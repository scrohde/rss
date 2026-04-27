package feed

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	feedUserAgentToken       = "pulserss"
	robotsTxtPath            = "/robots.txt"
	maxRobotsBodyBytes       = 512 * 1024
	robotsScannerInitBufSize = 4096
	robotsCacheTTL           = 24 * time.Hour
)

// RobotsBlockedError indicates feed polling was skipped due to robots policy.
type RobotsBlockedError struct {
	FeedURL      string
	RobotsURL    string
	DisallowRule string
}

// Error returns a user-facing message for robots policy blocks.
func (e *RobotsBlockedError) Error() string {
	return buildRobotsBlockedMessage(e.FeedURL, e.DisallowRule)
}

// Unwrap enables errors.Is checks against errFeedBlockedByRobots.
func (*RobotsBlockedError) Unwrap() error {
	return errFeedBlockedByRobots
}

type robotsRule struct {
	Pattern string
	Allow   bool
}

type robotsGroup struct {
	Rules  []robotsRule
	Agents []string
}

type robotsCheckResult struct {
	BlockedErr *RobotsBlockedError
}

type robotsParseState struct {
	groups       []robotsGroup
	currentGroup int
	inRuleZone   bool
}

type robotsDirective struct {
	Key   string
	Value string
}

type robotsPatternCandidate struct {
	Value    string
	Anchored bool
}

type robotsRuleDecision struct {
	Rule    string
	Allowed bool
}

type robotsCacheEntry struct {
	CachedAt   time.Time
	Body       string
	StatusCode int
}

type robotsCacheLookup struct {
	Entry robotsCacheEntry
	Fresh bool
	Found bool
}

type robotsPolicyCache struct {
	entries map[string]robotsCacheEntry
	mu      sync.RWMutex
}

//nolint:gochecknoglobals // Shared per-process robots cache avoids repeated network fetches.
var robotsCache = newRobotsPolicyCache()

func checkRobotsPolicy(ctx context.Context, feedURL string) (robotsCheckResult, error) {
	parsedFeedURL, ok := parseRobotsTargetURL(feedURL)
	if !ok {
		return allowRobotsResult(), nil
	}

	robotsURL := buildRobotsURL(parsedFeedURL)
	cacheKey := robotsCacheKey(parsedFeedURL)

	cacheLookup := robotsCache.lookup(cacheKey, time.Now().UTC())
	if cacheLookup.Found && cacheLookup.Fresh {
		return evaluateRobotsCacheEntry(cacheLookup.Entry, feedURL, robotsURL, parsedFeedURL), nil
	}

	resp, err := fetchRobotsResponse(ctx, robotsURL)
	if err != nil {
		return staleRobotsCacheResultOrError(cacheLookup, feedURL, robotsURL, parsedFeedURL, err)
	}

	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			slog.Warn("robots response close failed", logFieldFeedURL, feedURL, logFieldErr, closeErr)
		}
	}()

	result, entry, evalErr := evaluateRobotsResponse(resp, feedURL, robotsURL, parsedFeedURL)
	if evalErr != nil {
		return staleRobotsCacheResultOrError(cacheLookup, feedURL, robotsURL, parsedFeedURL, evalErr)
	}

	robotsCache.store(cacheKey, entry)

	return result, nil
}

func staleRobotsCacheResultOrError(
	cacheLookup robotsCacheLookup,
	feedURL string,
	robotsURL string,
	parsedFeedURL *url.URL,
	cause error,
) (robotsCheckResult, error) {
	if cacheLookup.Found {
		return evaluateRobotsCacheEntry(cacheLookup.Entry, feedURL, robotsURL, parsedFeedURL), nil
	}

	return allowRobotsResult(), cause
}

// CheckRobotsAllowed checks whether feed polling is allowed by robots.txt.
//
// If robots.txt cannot be fetched or parsed, the check is treated as non-blocking.
func CheckRobotsAllowed(ctx context.Context, feedURL string) error {
	result, err := checkRobotsPolicy(ctx, feedURL)
	if err != nil {
		slog.Warn(
			"robots policy check failed",
			logFieldFeedURL,
			feedURL,
			logFieldErr,
			err,
		)

		return nil
	}

	if result.BlockedErr != nil {
		return result.BlockedErr
	}

	return nil
}

func allowRobotsResult() robotsCheckResult {
	return robotsCheckResult{
		BlockedErr: nil,
	}
}

func evaluateRobotsCacheEntry(
	entry robotsCacheEntry,
	feedURL string,
	robotsURL string,
	parsedFeedURL *url.URL,
) robotsCheckResult {
	if entry.StatusCode < http.StatusOK || entry.StatusCode >= http.StatusMultipleChoices {
		return allowRobotsResult()
	}

	disallowRule := matchingDisallowRule(entry.Body, feedUserAgentToken, robotsPathForURL(parsedFeedURL))
	if disallowRule == "" {
		return allowRobotsResult()
	}

	return blockedRobotsResult(feedURL, robotsURL, disallowRule)
}

func newRobotsPolicyCache() *robotsPolicyCache {
	cache := new(robotsPolicyCache)
	cache.entries = make(map[string]robotsCacheEntry)

	return cache
}

func newRobotsCacheEntry(statusCode int, body string, cachedAt time.Time) robotsCacheEntry {
	return robotsCacheEntry{
		Body:       body,
		StatusCode: statusCode,
		CachedAt:   cachedAt,
	}
}

func newRobotsCacheLookup(entry robotsCacheEntry, fresh, found bool) robotsCacheLookup {
	return robotsCacheLookup{
		Entry: entry,
		Fresh: fresh,
		Found: found,
	}
}

func robotsCacheKey(feedURL *url.URL) string {
	return strings.ToLower(feedURL.Scheme) + "://" + strings.ToLower(feedURL.Host)
}

func (c *robotsPolicyCache) lookup(key string, now time.Time) robotsCacheLookup {
	c.mu.RLock()
	entry, found := c.entries[key]
	c.mu.RUnlock()

	if !found {
		return newRobotsCacheLookup(newRobotsCacheEntry(0, "", time.Time{}), false, false)
	}

	fresh := false
	if !entry.CachedAt.IsZero() {
		fresh = now.Sub(entry.CachedAt) <= robotsCacheTTL
	}

	return newRobotsCacheLookup(entry, fresh, true)
}

func (c *robotsPolicyCache) store(key string, entry robotsCacheEntry) {
	c.mu.Lock()
	c.entries[key] = entry
	c.mu.Unlock()
}

func parseRobotsTargetURL(feedURL string) (*url.URL, bool) {
	parsedFeedURL, err := url.Parse(feedURL)
	if err != nil {
		return nil, false
	}

	if parsedFeedURL.Scheme == "" || parsedFeedURL.Host == "" {
		return nil, false
	}

	return parsedFeedURL, true
}

func fetchRobotsResponse(ctx context.Context, robotsURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build robots request: %w", err)
	}

	req.Header.Set("User-Agent", feedUserAgent)

	resp, err := newFeedHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch robots.txt: %w", err)
	}

	return resp, nil
}

func evaluateRobotsResponse(
	resp *http.Response,
	feedURL string,
	robotsURL string,
	parsedFeedURL *url.URL,
) (robotsCheckResult, robotsCacheEntry, error) {
	now := time.Now().UTC()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return allowRobotsResult(), newRobotsCacheEntry(resp.StatusCode, "", now), nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRobotsBodyBytes))
	if err != nil {
		return allowRobotsResult(), newRobotsCacheEntry(0, "", time.Time{}), fmt.Errorf("read robots.txt: %w", err)
	}

	bodyText := string(body)

	disallowRule := matchingDisallowRule(bodyText, feedUserAgentToken, robotsPathForURL(parsedFeedURL))
	if disallowRule == "" {
		return allowRobotsResult(), newRobotsCacheEntry(resp.StatusCode, bodyText, now), nil
	}

	return blockedRobotsResult(feedURL, robotsURL, disallowRule),
		newRobotsCacheEntry(resp.StatusCode, bodyText, now),
		nil
}

func blockedRobotsResult(feedURL, robotsURL, disallowRule string) robotsCheckResult {
	return robotsCheckResult{
		BlockedErr: &RobotsBlockedError{
			FeedURL:      feedURL,
			RobotsURL:    robotsURL,
			DisallowRule: disallowRule,
		},
	}
}

func buildRobotsURL(feedURL *url.URL) string {
	robotsURL := *feedURL
	robotsURL.Path = robotsTxtPath
	robotsURL.RawPath = ""
	robotsURL.RawQuery = ""
	robotsURL.Fragment = ""

	return robotsURL.String()
}

func robotsPathForURL(feedURL *url.URL) string {
	path := feedURL.EscapedPath()
	if strings.TrimSpace(path) == "" {
		path = "/"
	}

	if strings.TrimSpace(feedURL.RawQuery) != "" {
		path += "?" + feedURL.RawQuery
	}

	return path
}

func matchingDisallowRule(robotsText, userAgent, targetPath string) string {
	groups := parseRobotsGroups(robotsText)
	if len(groups) == 0 {
		return ""
	}

	rules := matchingRules(groups, strings.ToLower(strings.TrimSpace(userAgent)))
	if len(rules) == 0 {
		return ""
	}

	decision := evaluateRuleDecision(rules, targetPath)
	if decision.Allowed {
		return ""
	}

	return decision.Rule
}

func parseRobotsGroups(robotsText string) []robotsGroup {
	scanner := bufio.NewScanner(strings.NewReader(robotsText))
	scanner.Buffer(make([]byte, 0, robotsScannerInitBufSize), maxRobotsBodyBytes)

	state := robotsParseState{
		groups:       make([]robotsGroup, 0),
		currentGroup: -1,
		inRuleZone:   false,
	}

	for scanner.Scan() {
		line := sanitizeRobotsLine(scanner.Text())
		if line == "" {
			state.resetGroup()

			continue
		}

		directive, ok := splitRobotsDirective(line)
		if !ok {
			continue
		}

		state.applyDirective(directive.Key, directive.Value)
	}

	return state.groups
}

func (s *robotsParseState) resetGroup() {
	s.currentGroup = -1
	s.inRuleZone = false
}

func (s *robotsParseState) applyDirective(key, value string) {
	switch key {
	case "user-agent":
		s.appendUserAgent(value)
	case "allow", "disallow":
		s.appendRule(key, value)
	default:
		return
	}
}

func (s *robotsParseState) appendUserAgent(value string) {
	normalizedAgent := strings.ToLower(strings.TrimSpace(value))
	if normalizedAgent == "" {
		return
	}

	if s.currentGroup == -1 || s.inRuleZone {
		s.startGroup()
	}

	s.groups[s.currentGroup].Agents = append(s.groups[s.currentGroup].Agents, normalizedAgent)
}

func (s *robotsParseState) appendRule(key, value string) {
	if s.currentGroup == -1 {
		return
	}

	pattern, patternOK := normalizeRobotsPattern(value)
	if !patternOK {
		return
	}

	s.groups[s.currentGroup].Rules = append(s.groups[s.currentGroup].Rules, robotsRule{
		Pattern: pattern,
		Allow:   key == "allow",
	})
	s.inRuleZone = true
}

func (s *robotsParseState) startGroup() {
	var group robotsGroup

	s.groups = append(s.groups, group)
	s.currentGroup = len(s.groups) - 1
	s.inRuleZone = false
}

func sanitizeRobotsLine(raw string) string {
	line := raw
	if idx := strings.Index(line, "#"); idx >= 0 {
		line = line[:idx]
	}

	return strings.TrimSpace(line)
}

func splitRobotsDirective(line string) (robotsDirective, bool) {
	key, value, found := strings.Cut(line, ":")
	if !found {
		return robotsDirective{
			Key:   "",
			Value: "",
		}, false
	}

	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)

	if key == "" {
		return robotsDirective{
			Key:   "",
			Value: "",
		}, false
	}

	return robotsDirective{
		Key:   key,
		Value: value,
	}, true
}

func normalizeRobotsPattern(rawPattern string) (string, bool) {
	pattern := strings.TrimSpace(rawPattern)
	if pattern == "" {
		return "", false
	}

	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}

	return pattern, true
}

func matchingRules(groups []robotsGroup, userAgent string) []robotsRule {
	bestMatchLen := -1
	groupMatchLens := make([]int, len(groups))

	for i, group := range groups {
		matchLen := userAgentMatchLength(group, userAgent)

		groupMatchLens[i] = matchLen
		if matchLen > bestMatchLen {
			bestMatchLen = matchLen
		}
	}

	if bestMatchLen < 0 {
		return nil
	}

	selected := make([]robotsRule, 0)

	for i, group := range groups {
		if groupMatchLens[i] != bestMatchLen {
			continue
		}

		selected = append(selected, group.Rules...)
	}

	return selected
}

func userAgentMatchLength(group robotsGroup, userAgent string) int {
	best := -1

	for _, rawAgent := range group.Agents {
		matchLen := singleAgentMatchLength(rawAgent, userAgent)
		if matchLen > best {
			best = matchLen
		}
	}

	return best
}

func singleAgentMatchLength(rawAgent, userAgent string) int {
	agent := strings.ToLower(strings.TrimSpace(rawAgent))
	if agent == "" {
		return -1
	}

	if agent == "*" {
		return 1
	}

	if strings.HasPrefix(userAgent, agent) {
		return len(agent)
	}

	return -1
}

func evaluateRuleDecision(rules []robotsRule, path string) robotsRuleDecision {
	bestLen := -1
	decision := robotsRuleDecision{
		Allowed: true,
		Rule:    "",
	}

	for _, candidate := range rules {
		matchLen, matched := robotsPatternMatchLength(candidate.Pattern, path)
		if !matched {
			continue
		}

		if shouldReplaceRule(bestLen, decision.Allowed, matchLen, candidate.Allow) {
			bestLen = matchLen
			decision.Allowed = candidate.Allow
			decision.Rule = candidate.Pattern
		}
	}

	if bestLen < 0 {
		return robotsRuleDecision{
			Allowed: true,
			Rule:    "",
		}
	}

	return decision
}

func shouldReplaceRule(bestLen int, bestAllowed bool, candidateLen int, candidateAllowed bool) bool {
	if candidateLen > bestLen {
		return true
	}

	return candidateLen == bestLen && candidateAllowed && !bestAllowed
}

func robotsPatternMatchLength(pattern, path string) (int, bool) {
	candidate, ok := normalizePatternForMatch(pattern)
	if !ok {
		return 0, false
	}

	if strings.Contains(candidate.Value, "*") {
		if candidate.Anchored {
			return anchoredWildcardPatternMatchLength(candidate.Value, path)
		}

		return wildcardPatternMatchLength(candidate.Value, path)
	}

	if candidate.Anchored {
		return anchoredLiteralPatternMatchLength(candidate.Value, path)
	}

	return literalPatternMatchLength(candidate.Value, path)
}

func normalizePatternForMatch(pattern string) (robotsPatternCandidate, bool) {
	candidate := strings.TrimSpace(pattern)
	if candidate == "" {
		return robotsPatternCandidate{
			Value:    "",
			Anchored: false,
		}, false
	}

	anchored := strings.HasSuffix(candidate, "$")
	if anchored {
		candidate = strings.TrimSuffix(candidate, "$")
	}

	if candidate == "" {
		return robotsPatternCandidate{
			Value:    "",
			Anchored: false,
		}, false
	}

	return robotsPatternCandidate{
		Value:    candidate,
		Anchored: anchored,
	}, true
}

func literalPatternMatchLength(pattern, path string) (int, bool) {
	if !strings.HasPrefix(path, pattern) {
		return 0, false
	}

	return len(pattern), true
}

func anchoredLiteralPatternMatchLength(pattern, path string) (int, bool) {
	if path != pattern {
		return 0, false
	}

	return len(pattern), true
}

func wildcardPatternMatchLength(pattern, path string) (int, bool) {
	expr := "^" + regexp.QuoteMeta(pattern)
	expr = strings.ReplaceAll(expr, "\\*", ".*")

	return compiledWildcardPatternMatchLength(expr, pattern, path)
}

func anchoredWildcardPatternMatchLength(pattern, path string) (int, bool) {
	expr := "^" + regexp.QuoteMeta(pattern)
	expr = strings.ReplaceAll(expr, "\\*", ".*")
	expr += "$"

	return compiledWildcardPatternMatchLength(expr, pattern, path)
}

func compiledWildcardPatternMatchLength(expr, pattern, path string) (int, bool) {
	matcher, err := regexp.Compile(expr)
	if err != nil || !matcher.MatchString(path) {
		return 0, false
	}

	return len(strings.ReplaceAll(pattern, "*", "")), true
}

func buildRobotsBlockedMessage(feedURL, disallowRule string) string {
	message := fmt.Sprintf("Polling blocked by robots.txt for %s.", feedURL)
	if strings.TrimSpace(disallowRule) != "" {
		message = fmt.Sprintf(
			"Polling blocked by robots.txt for %s (matched %q).",
			feedURL,
			disallowRule,
		)
	}

	guidance := sourceFallbackGuidance(feedURL)
	if guidance != "" {
		message += " " + guidance
	}

	return message
}

func sourceFallbackGuidance(feedURL string) string {
	parsedURL, err := url.Parse(feedURL)
	if err != nil {
		return ""
	}

	host := strings.ToLower(parsedURL.Hostname())
	if host == "reddit.com" || strings.HasSuffix(host, ".reddit.com") {
		return "This source may require an official API integration (for example Reddit's API)."
	}

	return ""
}

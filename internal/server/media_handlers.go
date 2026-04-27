package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rss/internal/content"
	"rss/internal/feed"
	"rss/internal/opml"
	"rss/internal/store"
)

func (a *App) handleExportOPML(w http.ResponseWriter, r *http.Request) {
	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	subscriptions := make([]opml.Subscription, 0, len(feeds))
	for _, listedFeed := range feeds {
		subscriptions = append(subscriptions, opml.Subscription{
			Title: listedFeed.Title,
			URL:   listedFeed.URL,
		})
	}

	filename := "pulse-rss-subscriptions-" + time.Now().UTC().Format("20060102") + ".opml"

	w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	err = opml.Write(w, "Pulse RSS Subscriptions", subscriptions)
	if err != nil {
		http.Error(w, "failed to export opml", http.StatusInternalServerError)

		return
	}
}

type opmlImportCounts struct {
	imported int
	skipped  int
}

func (a *App) handleImportOPML(w http.ResponseWriter, r *http.Request) {
	subscriptions, message := parseOPMLUpload(w, r)
	if message != "" {
		a.renderOPMLImportResponse(w, r, 0, 0, "error", message)

		return
	}

	counts := a.importOPMLSubscriptions(r.Context(), subscriptions)

	if counts.imported == 0 {
		a.renderOPMLImportResponse(
			w,
			r,
			counts.imported,
			counts.skipped,
			"error",
			"no valid feeds found in OPML",
		)

		return
	}

	a.renderOPMLImportResponse(w, r, counts.imported, counts.skipped, "success", "")
}

//nolint:gocritic // Tuple return keeps upload parsing call sites simple.
func parseOPMLUpload(w http.ResponseWriter, r *http.Request) ([]opml.Subscription, string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOPMLUploadBytes)

	parseErr := r.ParseMultipartForm(maxOPMLUploadBytes)
	if parseErr != nil {
		return nil, "invalid OPML upload"
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		return nil, "missing OPML file"
	}

	defer func() {
		closeErr := file.Close()
		if closeErr != nil {
			log.Printf("opml upload close: %v", closeErr)
		}
	}()

	subscriptions, err := opml.Parse(file)
	if err != nil {
		return nil, "invalid OPML file"
	}

	return subscriptions, ""
}

func (a *App) importOPMLSubscriptions(ctx context.Context, subscriptions []opml.Subscription) opmlImportCounts {
	var counts opmlImportCounts

	for _, subscription := range subscriptions {
		feedURL, err := feed.NormalizeURL(subscription.URL)
		if err != nil {
			counts.skipped++

			continue
		}

		feedTitle := subscribeFeedTitle(subscription.Title, feedURL)

		_, upsertErr := store.UpsertFeed(ctx, a.db, feedURL, feedTitle)
		if upsertErr != nil {
			counts.skipped++

			continue
		}

		counts.imported++
	}

	return counts
}

func (a *App) renderOPMLImportResponse(
	w http.ResponseWriter,
	r *http.Request,
	imported,
	skipped int,
	messageClass,
	fallbackMessage string,
) {
	feeds, ok := a.listFeedsOrError(w, r)
	if !ok {
		return
	}

	message := opmlImportMessage(imported, skipped, fallbackMessage)
	update := messageClass == "success"

	var data subscribeResponseData

	data.Message = message
	data.MessageClass = messageClass
	data.Feeds = feeds
	data.Update = update
	data.FeedEditMode = feedEditModeEnabled(r)
	a.renderTemplate(w, "opml_import_response", data)
}

func opmlImportMessage(imported, skipped int, fallbackMessage string) string {
	message := fallbackMessage
	if message == "" {
		message = "Imported " + strconv.Itoa(imported) + " feed"
		if imported != 1 {
			message += "s"
		}
	}

	if skipped > 0 {
		message += " (" + strconv.Itoa(skipped) + " skipped)"
	}

	return message
}

func (a *App) handleImageProxy(w http.ResponseWriter, r *http.Request) {
	target, ok := a.parseImageProxyTarget(w, r)
	if !ok {
		return
	}

	resp, ok := a.fetchImageProxyResponse(w, r, target)
	if !ok {
		return
	}

	defer closeImageProxyBody(resp)

	if !isSuccessfulImageProxyResponse(resp, target) {
		redirectToOriginalImage(w, r, target)

		return
	}

	result, ok := readImageProxyPayload(resp)
	if !ok {
		redirectToOriginalImage(w, r, target)

		return
	}

	writeImageProxyHeaders(w, resp, result.ContentType, len(result.Body))

	_, writeErr := w.Write(result.Body)
	if writeErr != nil {
		log.Printf("image proxy copy: %v", writeErr)
	}
}

type imageProxyPayload struct {
	ContentType string
	Body        []byte
}

func (a *App) parseImageProxyTarget(w http.ResponseWriter, r *http.Request) (*url.URL, bool) {
	raw := r.URL.Query().Get("url")
	if raw == "" {
		http.Error(w, "missing url", http.StatusBadRequest)

		return nil, false
	}

	if len(raw) > content.MaxImageProxyURLLength {
		http.Error(w, "url too long", http.StatusRequestURITooLong)

		return nil, false
	}

	target, err := url.Parse(raw)
	if err != nil || !content.IsAllowedResolvedProxyURL(r.Context(), target, a.imageProxyLookup) {
		http.Error(w, "invalid url", http.StatusBadRequest)

		return nil, false
	}

	return target, true
}

//nolint:gosec // Request target was validated by parseImageProxyTarget + proxy policy checks.
func (a *App) fetchImageProxyResponse(
	w http.ResponseWriter,
	r *http.Request,
	target *url.URL,
) (*http.Response, bool) {
	req, err := content.BuildImageProxyRequest(r.Context(), target)
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)

		return nil, false
	}

	resp, err := a.imageProxyClient.Do(req)
	if err != nil {
		redirectToOriginalImage(w, r, target)

		return nil, false
	}

	return resp, true
}

func closeImageProxyBody(resp *http.Response) {
	closeErr := resp.Body.Close()
	if closeErr != nil {
		log.Printf("image proxy close body: %v", closeErr)
	}
}

//nolint:gosec // Logged values are validated URL host/path for operational debugging.
func isSuccessfulImageProxyResponse(resp *http.Response, target *url.URL) bool {
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return true
	}

	slog.Debug(
		"image proxy upstream non-2xx",
		"status", resp.StatusCode,
		"target_host", target.Host,
		"target_path", target.EscapedPath(),
	)

	return false
}

func readImageProxyPayload(resp *http.Response) (imageProxyPayload, bool) {
	reader := bufio.NewReader(resp.Body)

	sniff, err := reader.Peek(imageProxySniffBytes)
	if err != nil && !errors.Is(err, io.EOF) {
		return emptyImageProxyPayload(), false
	}

	contentType, ok := imageProxyContentType(resp.Header.Get("Content-Type"), sniff)
	if !ok {
		return emptyImageProxyPayload(), false
	}

	body, err := io.ReadAll(io.LimitReader(reader, content.ImageProxyMaxBodyBytes+1))
	if err != nil {
		return emptyImageProxyPayload(), false
	}

	if int64(len(body)) > content.ImageProxyMaxBodyBytes {
		return emptyImageProxyPayload(), false
	}

	return imageProxyPayload{
		ContentType: contentType,
		Body:        body,
	}, true
}

func imageProxyContentType(headerValue string, sniff []byte) (string, bool) {
	contentType := headerValue
	if contentType != "" && strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return contentType, true
	}

	detected := http.DetectContentType(sniff)
	if !strings.HasPrefix(detected, "image/") {
		return "", false
	}

	return detected, true
}

func emptyImageProxyPayload() imageProxyPayload {
	return imageProxyPayload{
		ContentType: "",
		Body:        nil,
	}
}

func writeImageProxyHeaders(
	w http.ResponseWriter,
	resp *http.Response,
	contentType string,
	contentLength int,
) {
	w.Header().Set("Content-Type", contentType)

	if cacheControl := resp.Header.Get("Cache-Control"); cacheControl != "" {
		w.Header().Set("Cache-Control", cacheControl)
	} else {
		w.Header().Set("Cache-Control", content.ImageProxyCacheFallback)
	}

	if etag := resp.Header.Get("ETag"); etag != "" {
		w.Header().Set("ETag", etag)
	}

	if modified := resp.Header.Get("Last-Modified"); modified != "" {
		w.Header().Set("Last-Modified", modified)
	}

	w.Header().Set("Content-Length", strconv.Itoa(contentLength))
}

func redirectToOriginalImage(w http.ResponseWriter, r *http.Request, target *url.URL) {
	if target == nil {
		http.Error(w, "upstream fetch failed", http.StatusBadGateway)

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target.String(), http.StatusTemporaryRedirect)
}

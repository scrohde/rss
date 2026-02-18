// Package opml parses and writes OPML subscription lists.
package opml

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

const (
	opmlRootName = "opml"
	opmlVersion  = "2.0"
	xmlIndent    = "  "
)

// Subscription describes one feed entry in an OPML document.
type Subscription struct {
	Title string
	URL   string
}

type document struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr,omitempty"`
	Head    head     `xml:"head"`
	Body    body     `xml:"body"`
}

type head struct {
	Title string `xml:"title,omitempty"`
}

type body struct {
	Outlines []outline `xml:"outline"`
}

type outline struct {
	Text      string    `xml:"text,attr,omitempty"`
	Title     string    `xml:"title,attr,omitempty"`
	Type      string    `xml:"type,attr,omitempty"`
	XMLURL    string    `xml:"xmlUrl,attr,omitempty"`
	XMLURLAlt string    `xml:"xmlurl,attr,omitempty"`
	URL       string    `xml:"url,attr,omitempty"`
	Outlines  []outline `xml:"outline,omitempty"`
}

var errInvalidRoot = errors.New("invalid OPML: expected root <opml>")

// Parse decodes OPML data from r and returns discovered feed subscriptions.
func Parse(r io.Reader) ([]Subscription, error) {
	var doc document

	err := xml.NewDecoder(r).Decode(&doc)
	if err != nil {
		return nil, fmt.Errorf("invalid OPML: %w", err)
	}

	if !strings.EqualFold(doc.XMLName.Local, opmlRootName) {
		return nil, errInvalidRoot
	}

	var out []Subscription
	collectSubscriptions(doc.Body.Outlines, &out)

	return out, nil
}

// Write encodes subscriptions as an OPML document and writes it to writer.
func Write(writer io.Writer, title string, subscriptions []Subscription) error {
	doc := document{
		XMLName: xml.Name{
			Space: "",
			Local: opmlRootName,
		},
		Version: opmlVersion,
		Head:    head{Title: strings.TrimSpace(title)},
		Body:    body{Outlines: buildOutlines(subscriptions)},
	}

	_, err := io.WriteString(writer, xml.Header)
	if err != nil {
		return fmt.Errorf("write XML header: %w", err)
	}

	encoder := xml.NewEncoder(writer)

	defer func() {
		err = encoder.Close()
		if err != nil {
			slog.Warn("close OPML encoder", "err", err)
		}
	}()

	encoder.Indent("", xmlIndent)

	err = encoder.Encode(doc)
	if err != nil {
		return fmt.Errorf("encode OPML: %w", err)
	}

	flushErr := encoder.Flush()
	if flushErr != nil {
		return fmt.Errorf("flush OPML encoder: %w", flushErr)
	}

	return nil
}

func collectSubscriptions(outlines []outline, out *[]Subscription) {
	for index := range outlines {
		current := &outlines[index]
		appendOutlineSubscription(current, out)
		collectSubscriptions(current.Outlines, out)
	}
}

func buildOutlines(subscriptions []Subscription) []outline {
	var outlines []outline

	for _, subscription := range subscriptions {
		feedURL := strings.TrimSpace(subscription.URL)
		if feedURL == "" {
			continue
		}

		feedTitle := strings.TrimSpace(subscription.Title)
		if feedTitle == "" {
			feedTitle = feedURL
		}

		outlines = append(outlines, outline{
			Text:      feedTitle,
			Title:     feedTitle,
			Type:      "rss",
			XMLURL:    feedURL,
			XMLURLAlt: "",
			URL:       "",
			Outlines:  nil,
		})
	}

	return outlines
}

func appendOutlineSubscription(current *outline, out *[]Subscription) {
	feedURL := firstTrimmedValue(
		current.XMLURL,
		current.XMLURLAlt,
		current.URL,
	)
	if feedURL == "" {
		return
	}

	feedTitle := firstTrimmedValue(current.Title, current.Text)
	if feedTitle == "" {
		feedTitle = feedURL
	}

	*out = append(*out, Subscription{
		Title: feedTitle,
		URL:   feedURL,
	})
}

func firstTrimmedValue(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}

	return ""
}

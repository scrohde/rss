package opml

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

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

func Parse(r io.Reader) ([]Subscription, error) {
	var doc document
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid OPML: %w", err)
	}
	if !strings.EqualFold(doc.XMLName.Local, "opml") {
		return nil, fmt.Errorf("invalid OPML: expected root <opml>")
	}

	out := make([]Subscription, 0)
	collectSubscriptions(doc.Body.Outlines, &out)
	return out, nil
}

func Write(w io.Writer, title string, subscriptions []Subscription) error {
	outlines := make([]outline, 0, len(subscriptions))
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
			Text:   feedTitle,
			Title:  feedTitle,
			Type:   "rss",
			XMLURL: feedURL,
		})
	}

	doc := document{
		Version: "2.0",
		Head:    head{Title: strings.TrimSpace(title)},
		Body:    body{Outlines: outlines},
	}

	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}

	encoder := xml.NewEncoder(w)
	defer encoder.Close()
	encoder.Indent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return err
	}
	return encoder.Flush()
}

func collectSubscriptions(outlines []outline, out *[]Subscription) {
	for _, current := range outlines {
		feedURL := strings.TrimSpace(current.XMLURL)
		if feedURL == "" {
			feedURL = strings.TrimSpace(current.XMLURLAlt)
		}
		if feedURL == "" {
			feedURL = strings.TrimSpace(current.URL)
		}
		if feedURL != "" {
			feedTitle := strings.TrimSpace(current.Title)
			if feedTitle == "" {
				feedTitle = strings.TrimSpace(current.Text)
			}
			*out = append(*out, Subscription{
				Title: feedTitle,
				URL:   feedURL,
			})
		}
		if len(current.Outlines) > 0 {
			collectSubscriptions(current.Outlines, out)
		}
	}
}

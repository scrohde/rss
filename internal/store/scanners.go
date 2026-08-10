package store

import (
	"database/sql"
	"fmt"
	"time"

	"rss/internal/view"
)

func scanItemView(rows *sql.Rows) (view.ItemView, error) {
	var (
		id        int64
		title     string
		link      string
		summary   sql.NullString
		content   sql.NullString
		published sql.NullTime
		readAt    sql.NullTime
	)

	err := rows.Scan(&id, &title, &link, &summary, &content, &published, &readAt)
	if err != nil {
		return view.ItemView{}, fmt.Errorf("scan item row: %w", err)
	}

	return view.BuildItemView(id, title, link, summary, content, published, readAt), nil
}

func scanMobileStreamItemView(rows *sql.Rows) (view.ItemView, error) {
	var (
		id        int64
		feedID    int64
		feedTitle string
		title     string
		link      string
		summary   sql.NullString
		content   sql.NullString
		published sql.NullTime
	)

	err := rows.Scan(
		&id,
		&feedID,
		&feedTitle,
		&title,
		&link,
		&summary,
		&content,
		&published,
	)
	if err != nil {
		return view.ItemView{}, fmt.Errorf("scan mobile stream item row: %w", err)
	}

	item := view.BuildItemView(
		id,
		title,
		link,
		summary,
		content,
		published,
		sql.NullTime{Time: time.Time{}, Valid: false},
	)
	item.FeedID = feedID
	item.FeedTitle = feedTitle

	return item, nil
}

func scanFeedView(rows *sql.Rows) (view.FeedView, error) {
	var (
		id            int64
		sortOrder     int
		title         string
		originalTitle string
		url           string
		itemCount     int
		unreadCount   int
		lastChecked   sql.NullTime
		lastError     sql.NullString
	)

	err := rows.Scan(
		&id,
		&sortOrder,
		&title,
		&originalTitle,
		&url,
		&itemCount,
		&unreadCount,
		&lastChecked,
		&lastError,
	)
	if err != nil {
		return view.FeedView{}, fmt.Errorf("scan feed row: %w", err)
	}

	return view.BuildFeedView(
		id,
		sortOrder,
		title,
		originalTitle,
		url,
		itemCount,
		unreadCount,
		view.FeedStatus{
			LastChecked: lastChecked,
			LastError:   lastError,
		},
	), nil
}

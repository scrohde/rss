package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"rss/internal/view"
)

const (
	maxUnreadFeedPageSize = 20
	maxUnreadItemPageSize = 50
)

var (
	errInvalidUnreadPageLimit  = errors.New("invalid unread page limit")
	errInvalidUnreadFeedID     = errors.New("unread feed ID must be positive")
	errInvalidUnreadFeedCursor = errors.New("invalid unread feed cursor")
	errInvalidUnreadItemCursor = errors.New("invalid unread item cursor")
)

// UnreadFeedCursor identifies the last feed in a mobile aggregate section page.
type UnreadFeedCursor struct {
	SortOrder int
	FeedID    int64
}

// UnreadItemCursor identifies the last item in a feed-scoped unread page.
type UnreadItemCursor struct {
	SortKey string
	ItemID  int64
}

// UnreadFeedSection is one bounded feed section in the mobile aggregate.
type UnreadFeedSection struct {
	Next      *UnreadItemCursor
	FeedTitle string
	Items     []view.ItemView
	FeedID    int64
	SortOrder int
}

// UnreadFeedSectionsPage is one bounded page of mobile aggregate feed sections.
type UnreadFeedSectionsPage struct {
	Next     *UnreadFeedCursor
	Sections []UnreadFeedSection
}

// UnreadItemsPage is one bounded feed-scoped page of unread items.
type UnreadItemsPage struct {
	Next  *UnreadItemCursor
	Items []view.ItemView
}

const unreadFeedSectionsInitialSQL = `
WITH unread_feeds AS MATERIALIZED (
	SELECT
		f.id,
		f.sort_order,
		COALESCE(f.custom_title, f.title) AS feed_title
	FROM feeds f
	WHERE EXISTS (
		SELECT 1
		FROM items unread
		WHERE unread.feed_id = f.id
		  AND unread.read_at IS NULL
	)
	ORDER BY f.sort_order ASC, f.id ASC
	LIMIT ?
)
SELECT
	uf.id,
	uf.sort_order,
	uf.feed_title,
	i.id,
	i.title,
	i.link,
	i.summary,
	i.content,
	i.published_at,
	CAST(COALESCE(i.published_at, i.created_at) AS TEXT) AS item_sort_key
FROM unread_feeds uf
JOIN items i ON i.id IN (
	SELECT page_item.id
	FROM items page_item
	WHERE page_item.feed_id = uf.id
	  AND page_item.read_at IS NULL
	ORDER BY
		CAST(COALESCE(page_item.published_at, page_item.created_at) AS TEXT) DESC,
		page_item.id DESC
	LIMIT ?
)
ORDER BY
	uf.sort_order ASC,
	uf.id ASC,
	item_sort_key DESC,
	i.id DESC
`

const unreadFeedSectionsAfterSQL = `
WITH unread_feeds AS MATERIALIZED (
	SELECT
		f.id,
		f.sort_order,
		COALESCE(f.custom_title, f.title) AS feed_title
	FROM feeds f
	WHERE (
		f.sort_order > ?
		OR (f.sort_order = ? AND f.id > ?)
	)
	AND EXISTS (
		SELECT 1
		FROM items unread
		WHERE unread.feed_id = f.id
		  AND unread.read_at IS NULL
	)
	ORDER BY f.sort_order ASC, f.id ASC
	LIMIT ?
)
SELECT
	uf.id,
	uf.sort_order,
	uf.feed_title,
	i.id,
	i.title,
	i.link,
	i.summary,
	i.content,
	i.published_at,
	CAST(COALESCE(i.published_at, i.created_at) AS TEXT) AS item_sort_key
FROM unread_feeds uf
JOIN items i ON i.id IN (
	SELECT page_item.id
	FROM items page_item
	WHERE page_item.feed_id = uf.id
	  AND page_item.read_at IS NULL
	ORDER BY
		CAST(COALESCE(page_item.published_at, page_item.created_at) AS TEXT) DESC,
		page_item.id DESC
	LIMIT ?
)
ORDER BY
	uf.sort_order ASC,
	uf.id ASC,
	item_sort_key DESC,
	i.id DESC
`

const unreadItemsPageInitialSQL = `
SELECT
	i.id,
	i.feed_id,
	COALESCE(f.custom_title, f.title) AS feed_title,
	i.title,
	i.link,
	i.summary,
	i.content,
	i.published_at,
	CAST(COALESCE(i.published_at, i.created_at) AS TEXT) AS item_sort_key
FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE i.feed_id = ?
  AND i.read_at IS NULL
ORDER BY item_sort_key DESC, i.id DESC
LIMIT ?
`

const unreadItemsPageAfterSQL = `
SELECT
	i.id,
	i.feed_id,
	COALESCE(f.custom_title, f.title) AS feed_title,
	i.title,
	i.link,
	i.summary,
	i.content,
	i.published_at,
	CAST(COALESCE(i.published_at, i.created_at) AS TEXT) AS item_sort_key
FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE i.feed_id = ?
  AND i.read_at IS NULL
  AND (
	CAST(COALESCE(i.published_at, i.created_at) AS TEXT) < ?
	OR (
		CAST(COALESCE(i.published_at, i.created_at) AS TEXT) = ?
		AND i.id < ?
	)
  )
ORDER BY item_sort_key DESC, i.id DESC
LIMIT ?
`

// ListUnreadFeedSections returns a bounded page of unread feed sections in saved feed order.
func ListUnreadFeedSections(
	ctx context.Context,
	db *sql.DB,
	after *UnreadFeedCursor,
	feedLimit int,
	itemLimit int,
) (UnreadFeedSectionsPage, error) {
	ctx = contextOrBackground(ctx)

	err := validateUnreadSectionLimits(feedLimit, itemLimit)
	if err != nil {
		return UnreadFeedSectionsPage{}, err
	}

	rows, err := unreadFeedSectionRows(ctx, db, after, feedLimit+1, itemLimit+1)
	if err != nil {
		return UnreadFeedSectionsPage{}, err
	}

	sections, err := collectUnreadFeedSections(rows)
	if err != nil {
		return UnreadFeedSectionsPage{}, err
	}

	return buildUnreadFeedSectionsPage(sections, feedLimit, itemLimit), nil
}

// ListUnreadItemsByFeedPage returns a bounded newest-first unread page after the supplied item cursor.
func ListUnreadItemsByFeedPage(
	ctx context.Context,
	db *sql.DB,
	feedID int64,
	after *UnreadItemCursor,
	limit int,
) (UnreadItemsPage, error) {
	ctx = contextOrBackground(ctx)

	err := validateUnreadItemPageRequest(feedID, after, limit)
	if err != nil {
		return UnreadItemsPage{}, err
	}

	rows, err := unreadItemsPageRows(ctx, db, feedID, after, limit+1)
	if err != nil {
		return UnreadItemsPage{}, err
	}

	items, cursors, err := collectUnreadItemsPage(rows)
	if err != nil {
		return UnreadItemsPage{}, err
	}

	return buildUnreadItemsPage(items, cursors, limit), nil
}

func validateUnreadSectionLimits(feedLimit, itemLimit int) error {
	if feedLimit <= 0 || feedLimit > maxUnreadFeedPageSize {
		return fmt.Errorf("%w: feed limit must be between 1 and %d", errInvalidUnreadPageLimit,
			maxUnreadFeedPageSize)
	}

	if itemLimit <= 0 || itemLimit > maxUnreadItemPageSize {
		return fmt.Errorf("%w: item limit must be between 1 and %d", errInvalidUnreadPageLimit,
			maxUnreadItemPageSize)
	}

	return nil
}

func validateUnreadItemPageRequest(feedID int64, after *UnreadItemCursor, limit int) error {
	if feedID <= 0 {
		return errInvalidUnreadFeedID
	}

	if limit <= 0 || limit > maxUnreadItemPageSize {
		return fmt.Errorf("%w: item limit must be between 1 and %d", errInvalidUnreadPageLimit,
			maxUnreadItemPageSize)
	}

	if after != nil && (after.SortKey == "" || after.ItemID <= 0) {
		return errInvalidUnreadItemCursor
	}

	return nil
}

func unreadFeedSectionRows(
	ctx context.Context,
	db *sql.DB,
	after *UnreadFeedCursor,
	feedLimit int,
	itemLimit int,
) (*sql.Rows, error) {
	if after == nil {
		rows, err := db.QueryContext(ctx, unreadFeedSectionsInitialSQL, feedLimit, itemLimit)
		if err != nil {
			return nil, fmt.Errorf("query initial unread feed sections: %w", err)
		}

		return rows, nil
	}

	if after.SortOrder < 0 || after.FeedID <= 0 {
		return nil, errInvalidUnreadFeedCursor
	}

	rows, err := db.QueryContext(
		ctx,
		unreadFeedSectionsAfterSQL,
		after.SortOrder,
		after.SortOrder,
		after.FeedID,
		feedLimit,
		itemLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("query unread feed sections after feed %d: %w", after.FeedID, err)
	}

	return rows, nil
}

type unreadFeedSectionAccumulator struct {
	cursors []UnreadItemCursor
	section UnreadFeedSection
}

func collectUnreadFeedSections(rows *sql.Rows) ([]unreadFeedSectionAccumulator, error) {
	defer closeUnreadRows(rows)

	sections := make([]unreadFeedSectionAccumulator, 0)

	for rows.Next() {
		row, err := scanUnreadFeedSectionRow(rows)
		if err != nil {
			return nil, err
		}

		if len(sections) == 0 || sections[len(sections)-1].section.FeedID != row.feedID {
			sections = append(sections, unreadFeedSectionAccumulator{
				section: UnreadFeedSection{
					Next:      nil,
					FeedTitle: row.feedTitle,
					Items:     make([]view.ItemView, 0),
					FeedID:    row.feedID,
					SortOrder: row.sortOrder,
				},
				cursors: make([]UnreadItemCursor, 0),
			})
		}

		current := &sections[len(sections)-1]
		current.section.Items = append(current.section.Items, row.item)
		current.cursors = append(current.cursors, row.cursor)
	}

	err := rows.Err()
	if err != nil {
		return nil, fmt.Errorf("iterate unread feed section rows: %w", err)
	}

	return sections, nil
}

type unreadFeedSectionRow struct {
	cursor    UnreadItemCursor
	feedTitle string
	item      view.ItemView
	feedID    int64
	sortOrder int
}

func scanUnreadFeedSectionRow(rows *sql.Rows) (unreadFeedSectionRow, error) {
	var (
		feedID    int64
		sortOrder int
		feedTitle string
		itemID    int64
		title     string
		link      string
		summary   sql.NullString
		content   sql.NullString
		published sql.NullTime
		sortKey   string
	)

	err := rows.Scan(
		&feedID,
		&sortOrder,
		&feedTitle,
		&itemID,
		&title,
		&link,
		&summary,
		&content,
		&published,
		&sortKey,
	)
	if err != nil {
		return unreadFeedSectionRow{}, fmt.Errorf("scan unread feed section row: %w", err)
	}

	item := buildUnreadMobileItem(itemID, feedID, feedTitle, title, link, summary, content, published)

	return unreadFeedSectionRow{
		item:      item,
		cursor:    UnreadItemCursor{SortKey: sortKey, ItemID: itemID},
		feedTitle: feedTitle,
		feedID:    feedID,
		sortOrder: sortOrder,
	}, nil
}

func buildUnreadFeedSectionsPage(
	accumulators []unreadFeedSectionAccumulator,
	feedLimit int,
	itemLimit int,
) UnreadFeedSectionsPage {
	hasMoreFeeds := len(accumulators) > feedLimit
	if hasMoreFeeds {
		accumulators = accumulators[:feedLimit]
	}

	sections := make([]UnreadFeedSection, 0, len(accumulators))
	for index := range accumulators {
		section := accumulators[index].section
		if len(section.Items) > itemLimit {
			section.Items = section.Items[:itemLimit]
			cursor := accumulators[index].cursors[itemLimit-1]
			section.Next = &cursor
		}

		sections = append(sections, section)
	}

	page := UnreadFeedSectionsPage{Sections: sections, Next: nil}
	if hasMoreFeeds && len(sections) > 0 {
		last := sections[len(sections)-1]
		page.Next = &UnreadFeedCursor{SortOrder: last.SortOrder, FeedID: last.FeedID}
	}

	return page
}

func unreadItemsPageRows(
	ctx context.Context,
	db *sql.DB,
	feedID int64,
	after *UnreadItemCursor,
	limit int,
) (*sql.Rows, error) {
	if after == nil {
		rows, err := db.QueryContext(ctx, unreadItemsPageInitialSQL, feedID, limit)
		if err != nil {
			return nil, fmt.Errorf("query initial unread items for feed %d: %w", feedID, err)
		}

		return rows, nil
	}

	rows, err := db.QueryContext(
		ctx,
		unreadItemsPageAfterSQL,
		feedID,
		after.SortKey,
		after.SortKey,
		after.ItemID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query unread items for feed %d after item %d: %w", feedID, after.ItemID, err)
	}

	return rows, nil
}

func collectUnreadItemsPage(rows *sql.Rows) ([]view.ItemView, []UnreadItemCursor, error) {
	defer closeUnreadRows(rows)

	items := make([]view.ItemView, 0)
	cursors := make([]UnreadItemCursor, 0)

	for rows.Next() {
		item, cursor, err := scanUnreadItemPageRow(rows)
		if err != nil {
			return nil, nil, err
		}

		items = append(items, item)
		cursors = append(cursors, cursor)
	}

	err := rows.Err()
	if err != nil {
		return nil, nil, fmt.Errorf("iterate unread item page rows: %w", err)
	}

	return items, cursors, nil
}

func scanUnreadItemPageRow(rows *sql.Rows) (view.ItemView, UnreadItemCursor, error) {
	var (
		itemID    int64
		feedID    int64
		feedTitle string
		title     string
		link      string
		summary   sql.NullString
		content   sql.NullString
		published sql.NullTime
		sortKey   string
	)

	err := rows.Scan(
		&itemID,
		&feedID,
		&feedTitle,
		&title,
		&link,
		&summary,
		&content,
		&published,
		&sortKey,
	)
	if err != nil {
		return view.ItemView{}, UnreadItemCursor{}, fmt.Errorf("scan unread item page row: %w", err)
	}

	item := buildUnreadMobileItem(itemID, feedID, feedTitle, title, link, summary, content, published)

	return item, UnreadItemCursor{SortKey: sortKey, ItemID: itemID}, nil
}

func buildUnreadMobileItem(
	itemID int64,
	feedID int64,
	feedTitle string,
	title string,
	link string,
	summary sql.NullString,
	content sql.NullString,
	published sql.NullTime,
) view.ItemView {
	item := view.BuildItemView(
		itemID,
		title,
		link,
		summary,
		content,
		published,
		sql.NullTime{Time: time.Time{}, Valid: false},
	)
	item.FeedID = feedID
	item.FeedTitle = feedTitle

	return item
}

func buildUnreadItemsPage(
	items []view.ItemView,
	cursors []UnreadItemCursor,
	limit int,
) UnreadItemsPage {
	page := UnreadItemsPage{Items: items, Next: nil}
	if len(items) <= limit {
		return page
	}

	page.Items = items[:limit]
	cursor := cursors[limit-1]
	page.Next = &cursor

	return page
}

func closeUnreadRows(rows *sql.Rows) {
	err := rows.Close()
	if err != nil {
		slog.Warn("rows close failed", "err", err)
	}
}

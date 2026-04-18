package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"rss/internal/view"
)

type markAllReadUndoState struct {
	unreadItemIDs []int64
	feedID        int64
}

func (a *App) attachMarkAllReadUndo(itemList *view.ItemListData) *view.ItemListData {
	if itemList == nil {
		return nil
	}

	token, _, ok := a.activeMarkAllReadUndo(itemList.Feed.ID)
	if !ok {
		itemList.MarkAllReadUndoToken = ""

		return itemList
	}

	itemList.MarkAllReadUndoToken = token

	return itemList
}

func (a *App) storeMarkAllReadUndo(feedID int64, unreadItemIDs []int64) (string, error) {
	if len(unreadItemIDs) == 0 {
		a.clearMarkAllReadUndo(feedID)

		return "", nil
	}

	token, err := newMarkAllReadUndoToken()
	if err != nil {
		return "", err
	}

	a.markAllReadUndoMu.Lock()
	defer a.markAllReadUndoMu.Unlock()

	if existingToken, ok := a.markAllReadUndoTokenByFeed[feedID]; ok {
		delete(a.markAllReadUndoByToken, existingToken)
	}

	a.markAllReadUndoByToken[token] = markAllReadUndoState{
		feedID:        feedID,
		unreadItemIDs: append([]int64(nil), unreadItemIDs...),
	}
	a.markAllReadUndoTokenByFeed[feedID] = token

	return token, nil
}

func (a *App) clearMarkAllReadUndo(feedID int64) {
	a.markAllReadUndoMu.Lock()
	defer a.markAllReadUndoMu.Unlock()

	if token, ok := a.markAllReadUndoTokenByFeed[feedID]; ok {
		delete(a.markAllReadUndoByToken, token)
		delete(a.markAllReadUndoTokenByFeed, feedID)
	}
}

func (a *App) clearMarkAllReadUndoExcept(feedID int64) {
	a.markAllReadUndoMu.Lock()
	defer a.markAllReadUndoMu.Unlock()

	for currentFeedID, token := range a.markAllReadUndoTokenByFeed {
		if currentFeedID == feedID {
			continue
		}

		delete(a.markAllReadUndoByToken, token)
		delete(a.markAllReadUndoTokenByFeed, currentFeedID)
	}
}

func (a *App) activeMarkAllReadUndo(feedID int64) (string, markAllReadUndoState, bool) {
	a.markAllReadUndoMu.Lock()
	defer a.markAllReadUndoMu.Unlock()

	token, ok := a.markAllReadUndoTokenByFeed[feedID]
	if !ok {
		return "", markAllReadUndoState{feedID: 0, unreadItemIDs: nil}, false
	}

	state, ok := a.markAllReadUndoByToken[token]
	if !ok || state.feedID != feedID {
		delete(a.markAllReadUndoTokenByFeed, feedID)

		return "", markAllReadUndoState{feedID: 0, unreadItemIDs: nil}, false
	}

	return token, state, true
}

func (a *App) consumeMarkAllReadUndo(feedID int64, token string) ([]int64, bool) {
	a.markAllReadUndoMu.Lock()
	defer a.markAllReadUndoMu.Unlock()

	state, ok := a.markAllReadUndoByToken[token]
	if !ok || state.feedID != feedID {
		return nil, false
	}

	delete(a.markAllReadUndoByToken, token)

	currentToken, hasToken := a.markAllReadUndoTokenByFeed[feedID]
	if hasToken && currentToken == token {
		delete(a.markAllReadUndoTokenByFeed, feedID)
	}

	return append([]int64(nil), state.unreadItemIDs...), true
}

func newMarkAllReadUndoToken() (string, error) {
	var raw [16]byte

	_, err := rand.Read(raw[:])
	if err != nil {
		return "", fmt.Errorf("read mark-all-read undo token bytes: %w", err)
	}

	return hex.EncodeToString(raw[:]), nil
}

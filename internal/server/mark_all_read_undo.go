package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"rss/internal/view"
)

type markAllReadUndoState struct {
	changedItemIDs []int64
	feedID         int64
}

var (
	errMissingMarkAllReadUndoToken = errors.New("missing mark-all-read undo token")
	errEmptyUndoToken              = errors.New("undo token generator returned an empty token")
)

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

func (a *App) storeMarkAllReadUndo(feedID int64, changedItemIDs []int64, token string) error {
	if len(changedItemIDs) == 0 {
		a.clearMarkAllReadUndo(feedID)

		return nil
	}

	if token == "" {
		return errMissingMarkAllReadUndoToken
	}

	a.markAllReadUndoMu.Lock()
	defer a.markAllReadUndoMu.Unlock()

	if existingToken, ok := a.markAllReadUndoTokenByFeed[feedID]; ok {
		delete(a.markAllReadUndoByToken, existingToken)
	}

	a.markAllReadUndoByToken[token] = markAllReadUndoState{
		feedID:         feedID,
		changedItemIDs: append([]int64(nil), changedItemIDs...),
	}
	a.markAllReadUndoTokenByFeed[feedID] = token

	return nil
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
		return "", markAllReadUndoState{feedID: 0, changedItemIDs: nil}, false
	}

	state, ok := a.markAllReadUndoByToken[token]
	if !ok || state.feedID != feedID {
		delete(a.markAllReadUndoTokenByFeed, feedID)

		return "", markAllReadUndoState{feedID: 0, changedItemIDs: nil}, false
	}

	state.changedItemIDs = append([]int64(nil), state.changedItemIDs...)

	return token, state, true
}

func (a *App) markAllReadUndoForToken(feedID int64, token string) ([]int64, bool) {
	a.markAllReadUndoMu.Lock()
	defer a.markAllReadUndoMu.Unlock()

	state, ok := a.markAllReadUndoByToken[token]
	if !ok || state.feedID != feedID {
		return nil, false
	}

	return append([]int64(nil), state.changedItemIDs...), true
}

func (a *App) clearMarkAllReadUndoToken(feedID int64, token string) {
	a.markAllReadUndoMu.Lock()
	defer a.markAllReadUndoMu.Unlock()

	state, ok := a.markAllReadUndoByToken[token]
	if !ok || state.feedID != feedID {
		return
	}

	delete(a.markAllReadUndoByToken, token)

	currentToken, hasToken := a.markAllReadUndoTokenByFeed[feedID]
	if hasToken && currentToken == token {
		delete(a.markAllReadUndoTokenByFeed, feedID)
	}
}

func (a *App) generateUndoToken() (string, error) {
	generator := a.undoTokenGenerator
	if generator == nil {
		generator = newMarkAllReadUndoToken
	}

	token, err := generator()
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", errEmptyUndoToken
	}

	return token, nil
}

func newMarkAllReadUndoToken() (string, error) {
	var raw [16]byte

	_, err := rand.Read(raw[:])
	if err != nil {
		return "", fmt.Errorf("read mark-all-read undo token bytes: %w", err)
	}

	return hex.EncodeToString(raw[:]), nil
}

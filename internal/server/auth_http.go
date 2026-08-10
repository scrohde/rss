package server

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func decodePasskeyVerifyRequest(w http.ResponseWriter, r *http.Request) (passkeyVerifyRequest, []byte, error) {
	var request passkeyVerifyRequest

	r.Body = http.MaxBytesReader(w, r.Body, maxPasskeyJSONBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return passkeyVerifyRequest{}, nil, fmt.Errorf("read passkey verify body: %w", err)
	}

	err = json.Unmarshal(body, &request)
	if err != nil {
		return passkeyVerifyRequest{}, nil, fmt.Errorf("decode passkey verify body: %w", err)
	}

	if strings.TrimSpace(request.ChallengeID) == "" || len(request.Credential) == 0 {
		return passkeyVerifyRequest{}, nil, errMissingChallengeOrCred
	}

	return request, request.Credential, nil
}

func isRequestBodyTooLarge(err error) bool {
	maxBytesErr := new(http.MaxBytesError)

	return errors.As(err, &maxBytesErr)
}

func requestWithJSONBody(r *http.Request, body []byte) *http.Request {
	clone := r.Clone(r.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.Header = r.Header.Clone()
	clone.Header.Set("Content-Type", "application/json")

	return clone
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")

	encoder := json.NewEncoder(w)

	err := encoder.Encode(value)
	if err != nil {
		http.Error(w, "failed to write json", http.StatusInternalServerError)

		return
	}
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)

	_, err := rand.Read(buf)
	if err != nil {
		return "", fmt.Errorf("read random token bytes: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

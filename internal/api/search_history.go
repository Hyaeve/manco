package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

const (
	globalSearchHistoryKey   = "global_search_history"
	globalSearchHistoryLimit = 5
)

func normalizeGlobalSearchQuery(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if len(runes) > 200 {
		value = string(runes[:200])
	}
	return value
}

func mergeGlobalSearchHistory(history []string, query string) []string {
	query = normalizeGlobalSearchQuery(query)
	if query == "" {
		return append([]string(nil), history...)
	}
	merged := make([]string, 0, globalSearchHistoryLimit)
	merged = append(merged, query)
	for _, item := range history {
		item = normalizeGlobalSearchQuery(item)
		if item == "" || strings.EqualFold(item, query) {
			continue
		}
		merged = append(merged, item)
		if len(merged) == globalSearchHistoryLimit {
			break
		}
	}
	return merged
}

func (s *Server) readGlobalSearchHistory(ctx context.Context) ([]string, error) {
	raw, err := s.store.Setting(ctx, globalSearchHistoryKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return []string{}, err
	}
	var history []string
	if err := json.Unmarshal([]byte(raw), &history); err != nil {
		return []string{}, nil
	}
	merged := make([]string, 0, globalSearchHistoryLimit)
	for index := len(history) - 1; index >= 0; index-- {
		merged = mergeGlobalSearchHistory(merged, history[index])
	}
	return merged, nil
}

func (s *Server) recordGlobalSearch(ctx context.Context, query string) ([]string, error) {
	s.searchHistoryMu.Lock()
	defer s.searchHistoryMu.Unlock()

	current, err := s.readGlobalSearchHistory(ctx)
	if err != nil {
		return nil, err
	}
	history := mergeGlobalSearchHistory(current, query)
	if historyEqual(current, history) {
		return history, nil
	}
	raw, err := json.Marshal(history)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetSetting(ctx, globalSearchHistoryKey, string(raw)); err != nil {
		return nil, err
	}
	if s.configs != nil {
		_ = s.configs.PersistSettings(ctx, s.store)
	}
	return history, nil
}

func (s *Server) globalSearchHistory(ctx context.Context) ([]string, error) {
	s.searchHistoryMu.Lock()
	defer s.searchHistoryMu.Unlock()
	return s.readGlobalSearchHistory(ctx)
}

func (s *Server) handleGlobalSearchHistory(w http.ResponseWriter, r *http.Request) {
	history, err := s.globalSearchHistory(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": history})
}

func historyEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

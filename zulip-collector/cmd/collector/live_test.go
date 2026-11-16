package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"zulip-collector/internal/collector"
	"zulip-collector/internal/config"
)

func TestLiveResumeUpdateAndExport(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_URL")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_URL is not set")
	}
	t.Setenv("DATABASE_URL", dsn)
	ctx := t.Context()
	db, err := config.OpenPostgres(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var databaseName string
	if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&databaseName); err != nil || databaseName != "sop_collector_test" {
		t.Fatalf("unexpected test database %q: %v", databaseName, err)
	}

	base := time.Now().UnixMicro()
	channel, author, first := base, base+1, base+10
	toString := func(id int64) string { return strconv.FormatInt(id, 10) }
	scope := fmt.Sprintf("live:%d:2024-09-01:2024-09-01", channel)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := db.ExecContext(ctx, `
			DELETE FROM zulip.sync_state WHERE scope_id = $1;
		`, scope)
		if err != nil {
			t.Error(err)
		}
		for _, cleanup := range []struct {
			sql string
			id  int64
		}{
			{"DELETE FROM zulip.messages WHERE channel_id = $1", channel},
			{"DELETE FROM zulip.channels WHERE channel_id = $1", channel},
			{"DELETE FROM zulip.users WHERE user_id = $1", author},
		} {
			if _, err := db.ExecContext(ctx, cleanup.sql, cleanup.id); err != nil {
				t.Error(err)
			}
		}
	})

	from := time.Date(2024, 8, 31, 21, 0, 0, 0, time.UTC)
	until := from.Add(24 * time.Hour)
	const editedText = "Исправлено, \"текст\"\nНовая строка"
	var mutex sync.Mutex
	var anchors []string
	failSecond := true
	edited := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()

		reply := func(data map[string]any) {
			data["result"] = "success"
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(data); err != nil {
				t.Error(err)
			}
		}
		email, key, ok := r.BasicAuth()
		if !ok || email != "fixture@local" || key != "test-key" {
			t.Error("missing Basic authentication")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/streams":
			if r.URL.Query().Get("exclude_archived") != "false" {
				t.Error("archive discovery is disabled")
			}
			reply(map[string]any{"streams": []any{
				map[string]any{"stream_id": channel, "name": "БИВ24 Учебный канал", "invite_only": false},
				map[string]any{"stream_id": channel + 1, "name": "БИВ24 Закрытый канал", "invite_only": true},
			}})
		case "/api/v1/streams/" + toString(channel):
			reply(map[string]any{"stream": map[string]any{
				"stream_id": channel, "name": "БИВ24 Учебный канал", "invite_only": false,
			}})
		case "/api/v1/messages":
			q := r.URL.Query()
			anchor := q.Get("anchor")
			anchors = append(anchors, anchor)
			if q.Get("narrow") != fmt.Sprintf(`[{"operator":"channel","operand":%d}]`, channel) ||
				q.Get("apply_markdown") != "false" || q.Get("num_before") != "0" {
				t.Error("unexpected query", q.Encode())
			}
			msg := func(id int64, sent time.Time, text string, lastEdit *int64) map[string]any {
				return map[string]any{
					"id": id, "stream_id": channel, "type": "stream", "sender_id": author,
					"sender_full_name": "Автор", "timestamp": sent.Unix(), "content": text,
					"subject": "Тема / 100%+", "last_edit_timestamp": lastEdit,
				}
			}
			if anchor == "oldest" && q.Get("include_anchor") == "true" {
				reply(map[string]any{"found_newest": false, "messages": []any{
					msg(first-1, from.Add(-time.Second), "до начала", nil),
					msg(first, from, "первое", nil),
				}})
				return
			}
			if anchor != toString(first) || q.Get("include_anchor") != "false" {
				t.Error("unexpected anchor", anchor)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if failSecond {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			second := msg(first+1, until.Add(-time.Second), "второе", nil)
			if edited {
				stamp := until.Unix() + 60
				second = msg(first+1, until.Add(-time.Second), editedText, &stamp)
			}
			messages := []any{second, msg(first+2, until, "после конца", nil)}
			if edited {
				messages = append(messages, msg(first+3, until.Add(-time.Second), "добавлено после первого прохода", nil))
			}
			reply(map[string]any{"found_newest": true, "messages": messages})
		default:
			t.Error("unexpected API path", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	defaultTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = defaultTransport })
	t.Setenv("ZULIP_URL", server.URL)
	t.Setenv("ZULIP_EMAIL", "fixture@local")
	t.Setenv("ZULIP_API_KEY", "test-key")

	if err := listChannels(ctx, nil); err != nil {
		t.Fatal(err)
	}

	args := []string{"-channels", toString(channel), "-from", "2024-09-01", "-to", "2024-09-01"}
	if err := runLive(ctx, args); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("expected interruption, got %v", err)
	}
	var cursor string
	if err := db.QueryRowContext(ctx, "SELECT cursor FROM zulip.sync_state WHERE scope_id = $1", scope).Scan(&cursor); err != nil || cursor != toString(first) {
		t.Fatalf("checkpoint = %q, error = %v", cursor, err)
	}
	mutex.Lock()
	failSecond = false
	mutex.Unlock()
	for range 2 {
		if err := runLive(ctx, args); err != nil {
			t.Fatal(err)
		}
	}
	mutex.Lock()
	gotAnchors := append([]string(nil), anchors...)
	edited = true
	mutex.Unlock()
	wantAnchors := []string{"oldest", toString(first), toString(first), "oldest", toString(first)}
	if !reflect.DeepEqual(gotAnchors, wantAnchors) {
		t.Fatalf("anchors = %v, want %v", gotAnchors, wantAnchors)
	}
	if err := runLive(ctx, args); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	for _, format := range []string{"json", "csv"} {
		if err := runExport(ctx, append(append([]string(nil), args...), "-format", format, "-out", filepath.Join(dir, format))); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "json"))
	if err != nil {
		t.Fatal(err)
	}
	var messages []collector.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || messages[0].ID != first || !messages[0].CreatedAt.Equal(from) ||
		messages[1].ID != first+1 || messages[1].Text != editedText || messages[1].EditedAt == nil ||
		messages[1].AuthorName != "Автор" || messages[2].ID != first+3 ||
		!strings.HasSuffix(messages[1].URL, "/topic/.D0.A2.D0.B5.D0.BC.D0.B0.20.2F.20100.25.2B/near/"+toString(first+1)) {
		t.Fatalf("unexpected JSON export: %+v", messages)
	}
	file, err := os.Open(filepath.Join(dir, "csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	csvRows, err := csv.NewReader(file).ReadAll()
	if err != nil || len(csvRows) != 4 || csvRows[2][6] != messages[1].Text || csvRows[2][7] != messages[1].URL {
		t.Fatalf("unexpected CSV export: %v, error = %v", csvRows, err)
	}
}

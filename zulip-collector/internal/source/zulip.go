package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"zulip-collector/internal/collector"
)

type Zulip struct {
	baseURL *url.URL
	email   string
	key     string
	client  http.Client
}

func NewZulip(address, email, key string, client *http.Client) (*Zulip, error) {
	base, err := url.Parse(address)
	if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil ||
		base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return nil, fmt.Errorf("ZULIP_URL must be the HTTPS origin of the Zulip server")
	}
	if strings.TrimSpace(email) == "" || strings.Contains(email, ":") || strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("set ZULIP_EMAIL and ZULIP_API_KEY")
	}
	var httpClient http.Client
	if client != nil {
		httpClient = *client
	}
	httpClient.Timeout = 30 * time.Second
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Zulip{baseURL: base, email: email, key: key, client: httpClient}, nil
}

type Channel struct {
	ID   int64  `json:"stream_id"`
	Name string `json:"name"`
}

func (z *Zulip) CheckChannel(ctx context.Context, id int64) (Channel, error) {
	var response struct {
		Result string `json:"result"`
		Stream struct {
			Channel
			InviteOnly *bool `json:"invite_only"`
		} `json:"stream"`
	}
	err := z.get(ctx, "/streams/"+strconv.FormatInt(id, 10), nil, &response)
	if err != nil {
		return Channel{}, err
	}
	if response.Result != "success" || response.Stream.ID != id ||
		strings.TrimSpace(response.Stream.Name) == "" || response.Stream.InviteOnly == nil {
		return Channel{}, fmt.Errorf("invalid channel metadata from Zulip")
	}
	if *response.Stream.InviteOnly {
		return Channel{}, fmt.Errorf("channel is private; this collector accepts public organization channels")
	}
	return response.Stream.Channel, nil
}

type ZulipSource struct {
	API            *Zulip
	Channel        Channel
	From, Until    time.Time
	PageSize       int
	HistoryLimited bool
}

type zulipMessage struct {
	ID         int64  `json:"id"`
	Type       string `json:"type"`
	AuthorID   int64  `json:"sender_id"`
	AuthorName string `json:"sender_full_name"`
	ChannelID  int64  `json:"stream_id"`
	Topic      string `json:"subject"`
	Content    string `json:"content"`
	Timestamp  int64  `json:"timestamp"`
	LastEdit   int64  `json:"last_edit_timestamp"`
}

func (s *ZulipSource) FetchPage(ctx context.Context, cursor string) (collector.Page, error) {
	id := strconv.FormatInt(s.Channel.ID, 10)
	anchor := "oldest"
	includeAnchor := "true"
	var previous int64
	if cursor != "" {
		var err error
		previous, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || previous <= 0 {
			return collector.Page{}, fmt.Errorf("invalid Zulip cursor")
		}
		anchor = cursor
		includeAnchor = "false"
	}

	params := url.Values{
		"anchor":                 {anchor},
		"include_anchor":         {includeAnchor},
		"num_before":             {"0"},
		"num_after":              {strconv.Itoa(s.PageSize)},
		"narrow":                 {`[{"operator":"channel","operand":` + id + `}]`},
		"apply_markdown":         {"false"},
		"client_gravatar":        {"false"},
		"allow_empty_topic_name": {"true"},
	}
	var response struct {
		Result         string         `json:"result"`
		Messages       []zulipMessage `json:"messages"`
		FoundNewest    *bool          `json:"found_newest"`
		HistoryLimited bool           `json:"history_limited"`
	}
	if err := s.API.get(ctx, "/messages", params, &response); err != nil {
		return collector.Page{}, err
	}
	if response.Result != "success" || response.FoundNewest == nil || response.Messages == nil {
		return collector.Page{}, fmt.Errorf("invalid message page from Zulip")
	}
	s.HistoryLimited = s.HistoryLimited || response.HistoryLimited

	page := collector.Page{NextCursor: cursor, Done: *response.FoundNewest}
	for _, msg := range response.Messages {
		if msg.ID <= previous || msg.ChannelID != s.Channel.ID || msg.Type != "stream" ||
			msg.AuthorID <= 0 || strings.TrimSpace(msg.AuthorName) == "" || msg.Timestamp <= 0 {
			return collector.Page{}, fmt.Errorf("unexpected message on Zulip page (ID %d)", msg.ID)
		}
		previous = msg.ID
		page.NextCursor = strconv.FormatInt(msg.ID, 10)
		sent := time.Unix(msg.Timestamp, 0).UTC()
		if sent.Before(s.From) || !sent.Before(s.Until) {
			continue
		}
		record := collector.Message{
			ID: msg.ID, AuthorID: msg.AuthorID, AuthorName: msg.AuthorName,
			ChannelID: msg.ChannelID, ChannelName: s.Channel.Name, Topic: msg.Topic,
			Text: msg.Content, CreatedAt: sent,
			URL: s.API.baseURL.JoinPath("/").String() + "#narrow/channel/" + id +
				"/topic/" + encodeTopic(msg.Topic) + "/near/" + page.NextCursor,
		}
		if msg.LastEdit > 0 {
			edited := time.Unix(msg.LastEdit, 0).UTC()
			record.EditedAt = &edited
		}
		page.Messages = append(page.Messages, record)
	}
	return page, nil
}

func encodeTopic(topic string) string {
	return strings.NewReplacer(".", ".2E", "%", ".", "+", ".20").Replace(url.QueryEscape(topic))
}

func (z *Zulip) get(ctx context.Context, path string, params url.Values, value any) error {
	endpoint := z.baseURL.JoinPath("/api/v1" + path)
	endpoint.RawQuery = params.Encode()
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return err
		}
		req.SetBasicAuth(z.email, z.key)
		resp, err := z.client.Do(req)
		if err != nil {
			return fmt.Errorf("Zulip %s: %w", path, err)
		}
		status := resp.StatusCode
		if status == http.StatusOK {
			err := json.NewDecoder(resp.Body).Decode(value)
			resp.Body.Close()
			if err != nil {
				return fmt.Errorf("Zulip %s: invalid JSON response", path)
			}
			return nil
		}
		resp.Body.Close()
		if attempt >= 3 || (status != 429 && status != 502 && status != 503 && status != 504) {
			return fmt.Errorf("Zulip %s: HTTP %d", path, status)
		}
		wait := time.Second << attempt
		if retry := resp.Header.Get("Retry-After"); retry != "" {
			if seconds, err := strconv.Atoi(retry); err == nil && seconds >= 0 && seconds <= 60 {
				wait = time.Duration(seconds) * time.Second
			} else if date, err := http.ParseTime(retry); err == nil && time.Until(date) <= time.Minute {
				wait = time.Until(date)
			} else {
				return fmt.Errorf("Zulip %s: HTTP %d, retry after %s", path, status, retry)
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (z *Zulip) PublicChannels(ctx context.Context) ([]Channel, error) {
	params := url.Values{
		"include_public":     {"true"},
		"include_web_public": {"true"},
		"include_subscribed": {"false"},
		"exclude_archived":   {"false"},
	}
	var response struct {
		Result  string `json:"result"`
		Streams []struct {
			Channel
			InviteOnly *bool `json:"invite_only"`
		} `json:"streams"`
	}
	if err := z.get(ctx, "/streams", params, &response); err != nil {
		return nil, err
	}
	if response.Result != "success" || response.Streams == nil {
		return nil, fmt.Errorf("invalid channel list from Zulip")
	}
	channels := make([]Channel, 0, len(response.Streams))
	for _, stream := range response.Streams {
		if stream.InviteOnly != nil && !*stream.InviteOnly && stream.ID > 0 && stream.Name != "" {
			channels = append(channels, stream.Channel)
		}
	}
	return channels, nil
}

package postgres

import (
	"context"
	"database/sql"
	"time"

	"zulip-collector/internal/collector"
)

func (s *PostgresStore) WalkMessages(ctx context.Context, channels []int64, from, until time.Time, write func(collector.Message) error) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.message_id, m.author_id, u.display_name, m.channel_id, c.name,
		       m.topic, m.content, m.source_url, m.sent_at, m.edited_at
		FROM zulip.messages m
		JOIN zulip.users u ON u.user_id = m.author_id
		JOIN zulip.channels c ON c.channel_id = m.channel_id
		WHERE m.channel_id = ANY($1::bigint[])
		  AND m.sent_at >= $2 AND m.sent_at < $3
		ORDER BY m.sent_at, m.message_id
	`, channels, from, until)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var m collector.Message
		var edited sql.NullTime
		if err := rows.Scan(&m.ID, &m.AuthorID, &m.AuthorName, &m.ChannelID, &m.ChannelName,
			&m.Topic, &m.Text, &m.URL, &m.CreatedAt, &edited); err != nil {
			return err
		}
		m.CreatedAt = m.CreatedAt.UTC()
		if edited.Valid {
			value := edited.Time.UTC()
			m.EditedAt = &value
		}
		if err := write(m); err != nil {
			return err
		}
	}
	return rows.Err()
}

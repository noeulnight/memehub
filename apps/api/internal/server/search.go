package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/noeulnight/memehub/apps/api/internal/embedding"
)

// All list and search paths use the same filters, including private collection ownership.
const memeScopeSQL = `m.deleted_at IS NULL AND m.status='ready'
 AND ($1='' OR m.content_type=$1)
 AND NOT EXISTS(SELECT 1 FROM unnest($2::text[]) wanted(name) WHERE NOT EXISTS(
 SELECT 1 FROM meme_tags mt JOIN tags t ON t.id=mt.tag_id WHERE mt.meme_id=m.id AND t.normalized_name=wanted.name))
 AND ($3='' OR EXISTS(SELECT 1 FROM users u WHERE u.id=m.created_by AND u.oidc_issuer=$4 AND lower(u.username)=lower($3)))
 AND ($5::uuid IS NULL OR m.created_by=$5)
 AND ($6='' OR m.analysis_status=$6)
 AND ($9='' OR m.safety_rating=$9)
 AND ($7::uuid IS NULL OR EXISTS(SELECT 1 FROM collection_memes cm JOIN collections c ON c.id=cm.collection_id
 WHERE cm.meme_id=m.id AND c.id=$7 AND c.user_id=$8))`

// Normalize each field independently so matches cannot span unrelated fields.
const normalizedTextSQL = `search_normalize($11)<>'' AND (
 EXISTS(SELECT 1 FROM unnest(ARRAY[m.title,m.body_text,m.description,m.extracted_text,m.source_name,m.source_author]) field(value)
 WHERE strpos(search_normalize(field.value),search_normalize($11))>0)
 OR EXISTS(SELECT 1 FROM meme_tags mt JOIN tags t ON t.id=mt.tag_id
 WHERE mt.meme_id=m.id AND strpos(search_normalize(t.name),search_normalize($11))>0))`
const memeTextSQL = `concat_ws(' ',m.title,m.body_text,m.description,m.extracted_text,m.source_name,m.source_author) ILIKE $10
 OR EXISTS(SELECT 1 FROM meme_tags mt JOIN tags t ON t.id=mt.tag_id WHERE mt.meme_id=m.id AND t.name ILIKE $10)
 OR (` + normalizedTextSQL + `)`
const exactTextSQL = `lower(m.title)=lower($11) OR lower(m.extracted_text)=lower($11)
 OR EXISTS(SELECT 1 FROM meme_tags mt JOIN tags t ON t.id=mt.tag_id WHERE mt.meme_id=m.id AND lower(t.name)=lower($11))`
const textPrioritySQL = `CASE WHEN search_normalize($11)='' THEN 0
 WHEN search_normalize(m.extracted_text)=search_normalize($11)
 OR EXISTS(SELECT 1 FROM meme_tags mt JOIN tags t ON t.id=mt.tag_id WHERE mt.meme_id=m.id AND search_normalize(t.name)=search_normalize($11)) THEN 4
 WHEN strpos(search_normalize(m.extracted_text),search_normalize($11))>0
 OR EXISTS(SELECT 1 FROM meme_tags mt JOIN tags t ON t.id=mt.tag_id WHERE mt.meme_id=m.id AND strpos(search_normalize(t.name),search_normalize($11))>0) THEN 3
 WHEN search_normalize(m.title)=search_normalize($11) OR search_normalize(m.body_text)=search_normalize($11) THEN 2
 WHEN strpos(search_normalize(m.title),search_normalize($11))>0 OR strpos(search_normalize(m.body_text),search_normalize($11))>0 THEN 1
 ELSE 0 END`

type rankedMeme struct {
	id       uuid.UUID
	score    float64
	exact    bool
	priority int
}
type searchCursor struct {
	Search uuid.UUID `json:"search"`
	Offset int       `json:"offset"`
}

func searchScope(q string, args []any) string {
	data, _ := json.Marshal(struct {
		Query   string
		Filters []any
	}{q, args})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
func (s *Server) candidates(ctx context.Context, q string, args []any) ([]uuid.UUID, error) {
	pattern := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(q) + "%"
	rows, err := s.db.Query(ctx, `SELECT m.id,(`+exactTextSQL+`) AS exact,(`+textPrioritySQL+`) AS priority FROM memes m WHERE `+memeScopeSQL+` AND (`+memeTextSQL+`)
 ORDER BY exact DESC,priority DESC,m.created_at DESC,m.id DESC LIMIT 500`, append(args, pattern, q)...)
	if err != nil {
		return nil, err
	}
	ranks := map[uuid.UUID]*rankedMeme{}
	index := 0
	for rows.Next() {
		var id uuid.UUID
		var exact bool
		var priority int
		if err = rows.Scan(&id, &exact, &priority); err != nil {
			rows.Close()
			return nil, err
		}
		index++
		ranks[id] = &rankedMeme{id: id, score: 1 / (60 + float64(index)), exact: exact, priority: priority}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result, embedErr := s.embedder.Embed(ctx, q)
	if embedErr == nil && embedding.Valid(result.Vector) {
		// Exact scan preserves full-precision ranking. Add ANN only after recall and filtered latency checks.
		rows, err = s.db.Query(ctx, `SELECT m.id,1-(e.embedding OPERATOR(public.<=>) $10::public.vector) AS similarity
 FROM memes m JOIN meme_embeddings e ON e.meme_id=m.id
 WHERE `+memeScopeSQL+` AND e.content_revision=m.content_revision AND e.model=$11 AND e.input_version=$12
 ORDER BY e.embedding OPERATOR(public.<=>) $10::public.vector,m.id LIMIT 100`, append(args, embedding.Literal(result.Vector), embedding.Model, embedding.InputVersion)...)
		if err != nil {
			return nil, err
		}
		index = 0
		for rows.Next() {
			var id uuid.UUID
			var similarity float64
			if err = rows.Scan(&id, &similarity); err != nil {
				rows.Close()
				return nil, err
			}
			index++
			// A cutoff reduces noise; it is not a reliable detector of queries with no relevant answer.
			if similarity < 0.28 {
				continue
			}
			if ranks[id] == nil {
				ranks[id] = &rankedMeme{id: id}
			}
			ranks[id].score += 1 / (60 + float64(index))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	} else if ctx.Err() != nil {
		return nil, ctx.Err()
	} else {
		slog.Warn("semantic search unavailable; using keyword results")
	}
	sorted := make([]*rankedMeme, 0, len(ranks))
	for _, v := range ranks {
		sorted = append(sorted, v)
	}
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.exact != b.exact {
			return a.exact
		}
		if a.priority != b.priority {
			return a.priority > b.priority
		}
		if a.score != b.score {
			return a.score > b.score
		}
		return a.id.String() < b.id.String()
	})
	if len(sorted) > 500 {
		sorted = sorted[:500]
	}
	ids := make([]uuid.UUID, 0, len(sorted))
	for _, v := range sorted {
		ids = append(ids, v.id)
	}
	return ids, nil
}
func (s *Server) searchMemes(w http.ResponseWriter, r *http.Request, q string, limit int, args []any) {
	ctx := r.Context()
	scope := searchScope(q, args)
	var ids []uuid.UUID
	var c searchCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		data, err := base64.RawURLEncoding.DecodeString(raw)
		if len(raw) > 512 || err != nil || json.Unmarshal(data, &c) != nil || c.Search == uuid.Nil || c.Offset < 0 || c.Offset > 500 {
			problem(w, 400, "invalid_cursor")
			return
		}
		err = s.db.QueryRow(ctx, `SELECT meme_ids FROM meme_searches WHERE id=$1 AND scope_hash=$2 AND expires_at>now()`, c.Search, scope).Scan(&ids)
		if errors.Is(err, pgx.ErrNoRows) {
			problem(w, 400, "search_cursor_expired")
			return
		}
		if err != nil {
			internal(w)
			return
		}
	} else {
		var err error
		ids, err = s.candidates(ctx, q, args)
		if err != nil {
			internal(w)
			return
		}
		c.Search = uuid.New()
		// Bound storage for anonymous searches. Only hashes and IDs are retained.
		_, err = s.db.Exec(ctx, `DELETE FROM meme_searches WHERE expires_at<=now() OR id IN (SELECT id FROM meme_searches ORDER BY expires_at DESC OFFSET 1023)`)
		if err == nil {
			_, err = s.db.Exec(ctx, `INSERT INTO meme_searches(id,scope_hash,meme_ids) VALUES($1,$2,$3)`, c.Search, scope, ids)
		}
		if err != nil {
			internal(w)
			return
		}
	}
	if c.Offset > len(ids) {
		problem(w, 400, "invalid_cursor")
		return
	}
	// Reapply current visibility and collection membership, including removals after the first page.
	rows, err := s.db.Query(ctx, `SELECT `+memeColumns+`,p.ordinality FROM unnest($10::uuid[]) WITH ORDINALITY p(id,ordinality)
 JOIN memes m ON m.id=p.id WHERE `+memeScopeSQL+` AND p.ordinality>$11 ORDER BY p.ordinality LIMIT $12`, append(args, ids, c.Offset, limit+1)...)
	if err != nil {
		internal(w)
		return
	}
	defer rows.Close()
	items := []Meme{}
	var last int64
	more := false
	for rows.Next() {
		var m Meme
		var ordinal int64
		err = rows.Scan(&m.ID, &m.CreatedBy, &m.ContentType, &m.Title, &m.BodyText, &m.Description, &m.ExtractedText, &m.SourceURL, &m.SourceName, &m.SourceAuthor, &m.SourceNote, &m.Status, &m.AnalysisStatus, &m.Revision, &m.CreatedAt, &m.UpdatedAt, &m.Tags, &m.CreatedByName, &m.CreatedByUsername, &m.Width, &m.Height, &m.MIMEType, &m.SafetyRating, &ordinal)
		if err != nil {
			internal(w)
			return
		}
		if len(items) == limit {
			more = true
			break
		}
		items = append(items, m)
		last = ordinal
	}
	if rows.Err() != nil {
		internal(w)
		return
	}
	var next *string
	if more {
		c.Offset = int(last)
		data, _ := json.Marshal(c)
		value := base64.RawURLEncoding.EncodeToString(data)
		next = &value
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultPort                  = "8080"
	defaultDataDir               = "/data"
	defaultDBFileName            = "chartdb.sqlite"
	defaultMaxVersionsPerDiagram = 100
)

type app struct {
	db                    *sql.DB
	maxVersionsPerDiagram int
}

type diagramMeta struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	DatabaseType    string  `json:"databaseType"`
	DatabaseEdition *string `json:"databaseEdition,omitempty"`
	CreatedAt       string  `json:"createdAt"`
	UpdatedAt       string  `json:"updatedAt"`
}

type diagramVersion struct {
	ID        int64  `json:"id"`
	DiagramID string `json:"diagramId"`
	Name      string `json:"name"`
	Action    string `json:"action"`
	CreatedAt string `json:"createdAt"`
}

func main() {
	port := envOrDefault("PORT", defaultPort)
	dataDir := envOrDefault("DATA_DIR", defaultDataDir)
	maxVersions := envIntOrDefault("MAX_VERSIONS_PER_DIAGRAM", defaultMaxVersionsPerDiagram)

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	dbPath := filepath.Join(dataDir, defaultDBFileName)
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", dbPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if err := initSchema(db); err != nil {
		log.Fatalf("init schema: %v", err)
	}

	application := &app{
		db:                    db,
		maxVersionsPerDiagram: maxVersions,
	}

	handler := withCORS(application.routes())
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("backend is listening on :%s (db: %s)", port, dbPath)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}

func (a *app) routes() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/health":
			writeJSON(w, http.StatusOK, map[string]string{
				"status": "ok",
			})
			return
		case strings.HasPrefix(r.URL.Path, "/api/config"):
			a.handleConfig(w, r)
			return
		case strings.HasPrefix(r.URL.Path, "/api/diagrams"):
			a.handleDiagrams(w, r)
			return
		default:
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (a *app) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config, err := a.getConfig(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, config)
	case http.MethodPut:
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json payload")
			return
		}

		current, err := a.getConfig(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for k, v := range payload {
			current[k] = v
		}

		if err := a.setConfig(r.Context(), current); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, current)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *app) handleDiagrams(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")

	// /api/diagrams
	if len(parts) == 2 {
		switch r.Method {
		case http.MethodGet:
			full := r.URL.Query().Get("full") == "1" || r.URL.Query().Get("full") == "true"
			if full {
				payloads, err := a.listDiagramPayloads(r.Context())
				if err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				writeRawJSONArray(w, http.StatusOK, payloads)
				return
			}

			metas, err := a.listDiagramMetas(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, metas)
			return
		case http.MethodPost:
			payload, meta, err := decodeAndNormalizeDiagramPayload(r.Body)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}

			if err := a.insertDiagramWithVersion(r.Context(), payload, meta, "create"); err != nil {
				if isUniqueConstraintError(err) {
					writeError(w, http.StatusConflict, "diagram already exists")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}

			writeRawJSON(w, http.StatusCreated, payload)
			return
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}

	if len(parts) < 3 {
		writeError(w, http.StatusNotFound, "route not found")
		return
	}

	diagramID, err := url.PathUnescape(parts[2])
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid diagram id")
		return
	}

	// /api/diagrams/{id}
	if len(parts) == 3 {
		switch r.Method {
		case http.MethodGet:
			payload, err := a.getDiagramPayload(r.Context(), diagramID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, "diagram not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeRawJSON(w, http.StatusOK, payload)
			return
		case http.MethodPut:
			payload, meta, err := decodeAndNormalizeDiagramPayload(r.Body)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}

			if meta.ID != diagramID {
				writeError(w, http.StatusBadRequest, "diagram id in payload must match route id")
				return
			}

			if err := a.replaceDiagramWithVersion(r.Context(), diagramID, payload, meta, "save"); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, "diagram not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}

			writeRawJSON(w, http.StatusOK, payload)
			return
		case http.MethodPatch:
			patchData := map[string]interface{}{}
			if err := json.NewDecoder(r.Body).Decode(&patchData); err != nil {
				writeError(w, http.StatusBadRequest, "invalid json payload")
				return
			}

			updatedPayload, err := a.patchDiagramWithVersion(r.Context(), diagramID, patchData)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, "diagram not found")
					return
				}
				if isUniqueConstraintError(err) {
					writeError(w, http.StatusConflict, "diagram id already exists")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}

			writeRawJSON(w, http.StatusOK, updatedPayload)
			return
		case http.MethodDelete:
			if err := a.deleteDiagram(r.Context(), diagramID); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}

	// /api/diagrams/{id}/filter
	if len(parts) == 4 && parts[3] == "filter" {
		switch r.Method {
		case http.MethodGet:
			filter, err := a.getDiagramFilter(r.Context(), diagramID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, "filter not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeRawJSON(w, http.StatusOK, filter)
			return
		case http.MethodPut:
			var payload map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				writeError(w, http.StatusBadRequest, "invalid json payload")
				return
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid json payload")
				return
			}
			if err := a.setDiagramFilter(r.Context(), diagramID, raw); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeRawJSON(w, http.StatusOK, raw)
			return
		case http.MethodDelete:
			if err := a.deleteDiagramFilter(r.Context(), diagramID); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}

	// /api/diagrams/{id}/versions
	if len(parts) == 4 && parts[3] == "versions" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		versions, err := a.listVersions(r.Context(), diagramID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, versions)
		return
	}

	// /api/diagrams/{id}/versions/{versionId}
	if len(parts) == 5 && parts[3] == "versions" {
		versionID, err := strconv.ParseInt(parts[4], 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid version id")
			return
		}

		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		payload, err := a.getVersionPayload(r.Context(), diagramID, versionID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "version not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeRawJSON(w, http.StatusOK, payload)
		return
	}

	// /api/diagrams/{id}/versions/{versionId}/restore
	if len(parts) == 6 && parts[3] == "versions" && parts[5] == "restore" {
		versionID, err := strconv.ParseInt(parts[4], 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid version id")
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		payload, err := a.restoreVersion(r.Context(), diagramID, versionID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "version or diagram not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeRawJSON(w, http.StatusOK, payload)
		return
	}

	writeError(w, http.StatusNotFound, "route not found")
}

func (a *app) getConfig(ctx context.Context) (map[string]interface{}, error) {
	const query = `SELECT value FROM settings WHERE key = 'config'`
	var raw string
	err := a.db.QueryRowContext(ctx, query).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]interface{}{
			"defaultDiagramId": "",
		}, nil
	}
	if err != nil {
		return nil, err
	}

	config := map[string]interface{}{}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return nil, err
	}
	if _, ok := config["defaultDiagramId"]; !ok {
		config["defaultDiagramId"] = ""
	}
	return config, nil
}

func (a *app) setConfig(ctx context.Context, config map[string]interface{}) error {
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	const query = `
INSERT INTO settings (key, value)
VALUES ('config', ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`
	_, err = a.db.ExecContext(ctx, query, string(raw))
	return err
}

func (a *app) listDiagramMetas(ctx context.Context) ([]diagramMeta, error) {
	const query = `
SELECT id, name, database_type, database_edition, created_at, updated_at
FROM diagrams
ORDER BY updated_at DESC`
	rows, err := a.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]diagramMeta, 0)
	for rows.Next() {
		var item diagramMeta
		if err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.DatabaseType,
			&item.DatabaseEdition,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (a *app) listDiagramPayloads(ctx context.Context) ([][]byte, error) {
	const query = `SELECT payload FROM diagrams ORDER BY updated_at DESC`
	rows, err := a.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([][]byte, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		result = append(result, []byte(raw))
	}
	return result, rows.Err()
}

func (a *app) getDiagramPayload(ctx context.Context, diagramID string) ([]byte, error) {
	const query = `SELECT payload FROM diagrams WHERE id = ?`
	var raw string
	if err := a.db.QueryRowContext(ctx, query, diagramID).Scan(&raw); err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

func (a *app) insertDiagramWithVersion(ctx context.Context, payload []byte, meta diagramMeta, action string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	if err := insertDiagram(ctx, tx, payload, meta); err != nil {
		return err
	}
	if err := insertVersion(ctx, tx, meta.ID, meta.Name, payload, action); err != nil {
		return err
	}
	if err := pruneVersions(ctx, tx, meta.ID, a.maxVersionsPerDiagram); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *app) replaceDiagramWithVersion(ctx context.Context, diagramID string, payload []byte, meta diagramMeta, action string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	res, err := tx.ExecContext(ctx, `
UPDATE diagrams
SET name=?, database_type=?, database_edition=?, payload=?, updated_at=?
WHERE id=?`,
		meta.Name,
		meta.DatabaseType,
		meta.DatabaseEdition,
		string(payload),
		meta.UpdatedAt,
		diagramID,
	)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}

	if err := insertVersion(ctx, tx, diagramID, meta.Name, payload, action); err != nil {
		return err
	}
	if err := pruneVersions(ctx, tx, diagramID, a.maxVersionsPerDiagram); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *app) patchDiagramWithVersion(ctx context.Context, diagramID string, patch map[string]interface{}) ([]byte, error) {
	payload, err := a.getDiagramPayload(ctx, diagramID)
	if err != nil {
		return nil, err
	}

	var current map[string]interface{}
	if err := json.Unmarshal(payload, &current); err != nil {
		return nil, err
	}
	for k, v := range patch {
		current[k] = v
	}
	if _, ok := current["updatedAt"]; !ok {
		current["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	}

	updatedPayload, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}
	normalizedPayload, meta, err := normalizeDiagramPayload(updatedPayload)
	if err != nil {
		return nil, err
	}

	targetID := meta.ID
	if targetID == "" {
		targetID = diagramID
		meta.ID = diagramID
	}

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	if targetID == diagramID {
		res, err := tx.ExecContext(ctx, `
UPDATE diagrams
SET name=?, database_type=?, database_edition=?, payload=?, updated_at=?
WHERE id=?`,
			meta.Name,
			meta.DatabaseType,
			meta.DatabaseEdition,
			string(normalizedPayload),
			meta.UpdatedAt,
			diagramID,
		)
		if err != nil {
			return nil, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			return nil, sql.ErrNoRows
		}
	} else {
		res, err := tx.ExecContext(ctx, `
UPDATE diagrams
SET id=?, name=?, database_type=?, database_edition=?, payload=?, updated_at=?
WHERE id=?`,
			targetID,
			meta.Name,
			meta.DatabaseType,
			meta.DatabaseEdition,
			string(normalizedPayload),
			meta.UpdatedAt,
			diagramID,
		)
		if err != nil {
			return nil, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			return nil, sql.ErrNoRows
		}

		if _, err := tx.ExecContext(ctx, `UPDATE diagram_versions SET diagram_id=? WHERE diagram_id=?`, targetID, diagramID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE diagram_filters SET diagram_id=? WHERE diagram_id=?`, targetID, diagramID); err != nil {
			return nil, err
		}
	}

	if !isOnlyUpdatedAtPatch(patch) {
		if err := insertVersion(ctx, tx, targetID, meta.Name, normalizedPayload, "patch"); err != nil {
			return nil, err
		}
		if err := pruneVersions(ctx, tx, targetID, a.maxVersionsPerDiagram); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return normalizedPayload, nil
}

func (a *app) deleteDiagram(ctx context.Context, diagramID string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	if _, err := tx.ExecContext(ctx, `DELETE FROM diagram_filters WHERE diagram_id = ?`, diagramID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM diagram_versions WHERE diagram_id = ?`, diagramID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM diagrams WHERE id = ?`, diagramID); err != nil {
		return err
	}

	return tx.Commit()
}

func (a *app) getDiagramFilter(ctx context.Context, diagramID string) ([]byte, error) {
	const query = `SELECT payload FROM diagram_filters WHERE diagram_id = ?`
	var raw string
	if err := a.db.QueryRowContext(ctx, query, diagramID).Scan(&raw); err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

func (a *app) setDiagramFilter(ctx context.Context, diagramID string, payload []byte) error {
	const query = `
INSERT INTO diagram_filters (diagram_id, payload)
VALUES (?, ?)
ON CONFLICT(diagram_id) DO UPDATE SET payload=excluded.payload`
	_, err := a.db.ExecContext(ctx, query, diagramID, string(payload))
	return err
}

func (a *app) deleteDiagramFilter(ctx context.Context, diagramID string) error {
	_, err := a.db.ExecContext(ctx, `DELETE FROM diagram_filters WHERE diagram_id = ?`, diagramID)
	return err
}

func (a *app) listVersions(ctx context.Context, diagramID string) ([]diagramVersion, error) {
	const query = `
SELECT id, diagram_id, name, action, created_at
FROM diagram_versions
WHERE diagram_id = ?
ORDER BY id DESC`
	rows, err := a.db.QueryContext(ctx, query, diagramID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]diagramVersion, 0)
	for rows.Next() {
		item := diagramVersion{}
		if err := rows.Scan(&item.ID, &item.DiagramID, &item.Name, &item.Action, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (a *app) getVersionPayload(ctx context.Context, diagramID string, versionID int64) ([]byte, error) {
	const query = `
SELECT payload
FROM diagram_versions
WHERE diagram_id = ? AND id = ?`
	var raw string
	if err := a.db.QueryRowContext(ctx, query, diagramID, versionID).Scan(&raw); err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

func (a *app) restoreVersion(ctx context.Context, diagramID string, versionID int64) ([]byte, error) {
	versionPayload, err := a.getVersionPayload(ctx, diagramID, versionID)
	if err != nil {
		return nil, err
	}

	normalizedPayload, meta, err := normalizeDiagramPayload(versionPayload)
	if err != nil {
		return nil, err
	}
	meta.ID = diagramID
	meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)

	restoredMap := map[string]interface{}{}
	if err := json.Unmarshal(normalizedPayload, &restoredMap); err != nil {
		return nil, err
	}
	restoredMap["id"] = diagramID
	restoredMap["updatedAt"] = meta.UpdatedAt

	restoredPayload, err := json.Marshal(restoredMap)
	if err != nil {
		return nil, err
	}
	restoredPayload, meta, err = normalizeDiagramPayload(restoredPayload)
	if err != nil {
		return nil, err
	}

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	res, err := tx.ExecContext(ctx, `
UPDATE diagrams
SET name=?, database_type=?, database_edition=?, payload=?, updated_at=?
WHERE id=?`,
		meta.Name,
		meta.DatabaseType,
		meta.DatabaseEdition,
		string(restoredPayload),
		meta.UpdatedAt,
		diagramID,
	)
	if err != nil {
		return nil, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, sql.ErrNoRows
	}

	if err := insertVersion(ctx, tx, diagramID, meta.Name, restoredPayload, "restore"); err != nil {
		return nil, err
	}
	if err := pruneVersions(ctx, tx, diagramID, a.maxVersionsPerDiagram); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return restoredPayload, nil
}

func insertDiagram(ctx context.Context, tx *sql.Tx, payload []byte, meta diagramMeta) error {
	const query = `
INSERT INTO diagrams (id, name, database_type, database_edition, payload, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := tx.ExecContext(
		ctx,
		query,
		meta.ID,
		meta.Name,
		meta.DatabaseType,
		meta.DatabaseEdition,
		string(payload),
		meta.CreatedAt,
		meta.UpdatedAt,
	)
	return err
}

func insertVersion(ctx context.Context, tx *sql.Tx, diagramID, diagramName string, payload []byte, action string) error {
	const query = `
INSERT INTO diagram_versions (diagram_id, name, payload, action, created_at)
VALUES (?, ?, ?, ?, ?)`
	_, err := tx.ExecContext(
		ctx,
		query,
		diagramID,
		diagramName,
		string(payload),
		action,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func pruneVersions(ctx context.Context, tx *sql.Tx, diagramID string, keep int) error {
	if keep <= 0 {
		return nil
	}

	const query = `
DELETE FROM diagram_versions
WHERE id IN (
	SELECT id
	FROM diagram_versions
	WHERE diagram_id = ?
	ORDER BY id DESC
	LIMIT -1 OFFSET ?
)`
	_, err := tx.ExecContext(ctx, query, diagramID, keep)
	return err
}

func decodeAndNormalizeDiagramPayload(bodyReader interface {
	Read(p []byte) (n int, err error)
}) ([]byte, diagramMeta, error) {
	var payload map[string]interface{}
	if err := json.NewDecoder(bodyReader).Decode(&payload); err != nil {
		return nil, diagramMeta{}, errors.New("invalid json payload")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, diagramMeta{}, errors.New("invalid json payload")
	}
	return normalizeDiagramPayload(raw)
}

func normalizeDiagramPayload(raw []byte) ([]byte, diagramMeta, error) {
	data := map[string]interface{}{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, diagramMeta{}, errors.New("invalid json payload")
	}

	id, ok := asString(data["id"])
	if !ok || strings.TrimSpace(id) == "" {
		return nil, diagramMeta{}, errors.New("diagram.id is required")
	}
	name, ok := asString(data["name"])
	if !ok || strings.TrimSpace(name) == "" {
		return nil, diagramMeta{}, errors.New("diagram.name is required")
	}
	databaseType, ok := asString(data["databaseType"])
	if !ok || strings.TrimSpace(databaseType) == "" {
		return nil, diagramMeta{}, errors.New("diagram.databaseType is required")
	}

	nowISO := time.Now().UTC().Format(time.RFC3339Nano)

	createdAt, createdOK := asString(data["createdAt"])
	if !createdOK || createdAt == "" {
		createdAt = nowISO
		data["createdAt"] = createdAt
	}

	updatedAt, updatedOK := asString(data["updatedAt"])
	if !updatedOK || updatedAt == "" {
		updatedAt = nowISO
		data["updatedAt"] = updatedAt
	}

	var databaseEdition *string
	if value, exists := data["databaseEdition"]; exists && value != nil {
		if edition, isString := asString(value); isString {
			databaseEdition = &edition
		}
	}

	normalized, err := json.Marshal(data)
	if err != nil {
		return nil, diagramMeta{}, errors.New("invalid json payload")
	}

	meta := diagramMeta{
		ID:              id,
		Name:            name,
		DatabaseType:    databaseType,
		DatabaseEdition: databaseEdition,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}
	return normalized, meta, nil
}

func initSchema(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS diagrams (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	database_type TEXT NOT NULL,
	database_edition TEXT,
	payload TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS diagram_versions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	diagram_id TEXT NOT NULL,
	name TEXT NOT NULL,
	payload TEXT NOT NULL,
	action TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_diagram_versions_diagram_id_id
ON diagram_versions(diagram_id, id DESC);

CREATE TABLE IF NOT EXISTS diagram_filters (
	diagram_id TEXT PRIMARY KEY,
	payload TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);`
	_, err := db.Exec(schema)
	return err
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeRawJSON(w http.ResponseWriter, status int, payload []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func writeRawJSONArray(w http.ResponseWriter, status int, payloads [][]byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte("["))
	for i, payload := range payloads {
		if i > 0 {
			_, _ = w.Write([]byte(","))
		}
		_, _ = w.Write(payload)
	}
	_, _ = w.Write([]byte("]"))
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{
		"error": message,
	})
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envIntOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func isUniqueConstraintError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique")
}

func isOnlyUpdatedAtPatch(patch map[string]interface{}) bool {
	if len(patch) != 1 {
		return false
	}
	_, ok := patch["updatedAt"]
	return ok
}

func asString(v interface{}) (string, bool) {
	value, ok := v.(string)
	return value, ok
}

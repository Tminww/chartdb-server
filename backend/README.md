# ChartDB Backend

Simple Go backend for ChartDB fork:

- SQLite storage
- Shared diagrams for all users
- Diagram version history with restore API

## Environment Variables

- `PORT` (default `8080`)
- `DATA_DIR` (default `/data`)
- `MAX_VERSIONS_PER_DIAGRAM` (default `100`)

## Local run

```bash
go run ./main.go
```

## API

- `GET /api/health`
- `GET /api/config`
- `PUT /api/config`
- `GET /api/diagrams`
- `GET /api/diagrams?full=1`
- `POST /api/diagrams`
- `GET /api/diagrams/:id`
- `PUT /api/diagrams/:id`
- `PATCH /api/diagrams/:id`
- `DELETE /api/diagrams/:id`
- `GET /api/diagrams/:id/filter`
- `PUT /api/diagrams/:id/filter`
- `DELETE /api/diagrams/:id/filter`
- `GET /api/diagrams/:id/versions`
- `GET /api/diagrams/:id/versions/:versionId`
- `POST /api/diagrams/:id/versions/:versionId/restore`

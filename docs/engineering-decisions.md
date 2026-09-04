# Engineering decisions

The choices below are useful interview material because each one makes an explicit trade-off rather than hiding it behind a feature list.

| Decision | Chosen approach | Rejected or deferred alternative | Trade-off |
| --- | --- | --- | --- |
| Serving runtime | Go `net/http` with four direct third-party modules | A larger web framework | More explicit code and a smaller dependency surface; fewer framework conveniences |
| Integration boundary | ETL and API couple through PostgreSQL schemas | Shared in-process domain objects | Independent ingestion/serving lifecycles; schema changes require discipline |
| Parcel geometry | `MultiPolygon` in PostGIS | `Polygon` only | Handles split parcels correctly; slightly more complex geometry handling |
| Reverse geocoding | Nearest building for road address, containing parcel for legal-dong identity | One nearest-object heuristic | Better behavior near administrative boundaries; requires two spatial queries |
| Place search | Exact/prefix/trigram stages and optional spatial filtering | Global similarity sort | Predictable latency on short Korean queries; more query branches |
| UI deployment | Compile static UI into the Go binary | Separate frontend origin | One artifact and no bundled-client CORS setup; UI release is tied to backend release |
| Abuse controls | IP limiter before DB-backed authentication, then per-key limiter | Authenticate every request first | Bounds unauthenticated floods; IP sharing can create coarse contention |
| Request tracing | Request ID outside the protected API chain | Generate IDs inside handlers | Captures auth/rate-limit failures; applies only to `/v1` routes |
| Usage collection | Bounded, asynchronous, batch meter | Synchronous durable write per request | Preserves latency and availability; not lossless billing semantics |
| Shutdown | Drain HTTP before draining the meter | Stop workers immediately | Retains events produced by in-flight requests; shutdown can take up to the deadline |
| Observability | Sentry is operationally optional and scrubbed | Fail startup when telemetry is unavailable | Service remains available; telemetry gaps must be tolerated |
| Data lineage | `source` and `source_version` required on primary serving tables | One undocumented global dataset version | Row-level provenance; version meaning remains source-specific |
| MCP surface | Thin seven-tool adapter over REST | Duplicate domain logic inside the agent server | One source of domain behavior; MCP availability depends on the upstream API |
| Public release | Fresh curated history and file allowlist | Mirror the internal development repository | Reduces disclosure risk; omits some development context by design |

## A note on deterministic outputs

The geospatial serving path is designed to return public-source facts and transparent calculations. It does not produce automated appraisal judgments or fill missing regional data with model estimates. Missing regional coverage is reported as `REGION_NOT_LOADED`.

This boundary is technical and product-facing: downstream users can decide how to combine facts, while the platform keeps provenance and uncertainty visible.

## What remains deliberately imperfect

- The asynchronous usage meter can drop events under pressure.
- Raw datasets are not bundled, so local end-to-end API reproduction requires separately licensed/acquired data.
- DB-backed integration test coverage is smaller than parser and middleware unit coverage.
- The MCP adapter intentionally covers tool discovery/calls rather than the entire protocol surface.
- Some regional layers are loaded incrementally.

These are stated rather than disguised because they define the next engineering work more accurately than a generic roadmap.


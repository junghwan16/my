# Vendored assets

These files are checked in so the `gieok web` binary renders fully offline with
no CDN or runtime download (ADR-0008: embed + vendoring). They are served from
`/vendor/` by the same `go:embed static` that serves the pages.

## cytoscape.min.js

- Library: [Cytoscape.js](https://js.cytoscape.org/), a graph rendering library.
- Version: 3.30.2
- License: MIT (the full license header is retained at the top of the file).
- Source: https://unpkg.com/cytoscape@3.30.2/dist/cytoscape.min.js

Used by `/graph.html` to render the scope-scoped provenance graph (Source and
Memory nodes with Link edges), with per-Memory Link fan-in driving node size.

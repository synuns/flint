// realstore_diag.go — realStore method extraction file accompanying the wave-1 ops
// move (doctor, audit, diff, status,
// tuning) — the realstore_* convention. Investigation found no realStore method definitions in
// these five files (all reads go through queryOnce — owned by realstore_write, read, curation,
// and root.go). Nothing to move, so the body is left empty.
package cli

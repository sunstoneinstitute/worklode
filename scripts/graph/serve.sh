#!/usr/bin/env bash
# Export the task graph and serve it over SPARQL with HornDB.
#
# Two endpoints, deliberately:
#   :3840  raw          - asserted triples only. Property paths (wl:blocks+) do
#                         the closure at query time, so direct and transitive
#                         edges stay distinguishable. Needed for "what can I
#                         start now", which turns on direct blockers only.
#   :3841  materialized - OWL 2 RL forward chaining. prp-trp closes wl:blocks
#                         and prp-inv1/2 derives wl:dependsOn, both from axioms
#                         already in ns/ontology.ttl.
#
# Both are in-memory and load once: re-exporting means restarting them.
set -euo pipefail

HORNDB=${HORNDB:-serve}
DIR=${LODE_GRAPH_DIR:-/tmp/lode-graph}
REPO=$(git rev-parse --show-toplevel)

mkdir -p "$DIR"

# Stop only the servers this script started; `serve` is too common a program
# name to match by name.
for pidfile in "$DIR"/raw.pid "$DIR"/materialized.pid; do
	[ -f "$pidfile" ] && kill "$(cat "$pidfile")" 2>/dev/null || true
	rm -f "$pidfile"
done

"$REPO/bin/lode" graph triples --project= -o "$DIR/tasks.nt"
cp "$REPO/ns/ontology.ttl" "$REPO/ns/concept.ttl" "$DIR/"
# shapes.ttl is SHACL, which HornDB does not evaluate, so it is left out.

nohup "$HORNDB" --data "$DIR" --bind 127.0.0.1:3840 > "$DIR/raw.log" 2>&1 &
echo $! > "$DIR/raw.pid"
nohup "$HORNDB" --data "$DIR" --materialize --bind 127.0.0.1:3841 > "$DIR/materialized.log" 2>&1 &
echo $! > "$DIR/materialized.pid"

echo "exported $(wc -l < "$DIR/tasks.nt") triples to $DIR/tasks.nt"
echo "raw          http://127.0.0.1:3840/query  (log $DIR/raw.log)"
echo "materialized http://127.0.0.1:3841/query  (log $DIR/materialized.log)"
echo "materialization takes about a minute; watch for 'triples loaded' in its log."

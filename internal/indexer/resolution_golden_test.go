package indexer

import (
	"database/sql"
	"testing"
)

// goldenEdge is a hand-labeled call edge anchored on a real name collision in
// the self-repo. Each names a call site (caller function + callee name + the
// file the call is written in) and the definition it SHOULD resolve to once the
// scope resolver lands. These are the edges name-global resolution can get
// wrong, so they are the precision regression gate.
type goldenEdge struct {
	desc           string
	callerName     string // enclosing function of the call site
	calleeName     string // captured callee
	callSiteFile   string // file the call is written in (calls.file_path)
	wantReceiver   string // expected functions.receiver_type of the target ("" for plain func)
	wantTargetPkg  string // expected functions.package of the target
	wantTargetFile string // expected functions.file_path of the target
	wantMarker     string // expected calls.resolution marker
}

// goldenEdges anchors on the three true cross-file collisions in the repo:
//   - Register: method on *Flags (config) vs *Handler (mcp)  -> receiver-typed
//   - Run:      setup.Run vs bench.Run, called package-qualified -> cross-package
//     (each links to ITS OWN package's Run via the imported package segment; the
//     collision risk — setup.Run mislinking to bench.Run — is verified absent).
var goldenEdges = []goldenEdge{
	{
		desc:           "cfg.Register in main.go -> config.Flags.Register (not mcp.Handler.Register)",
		callerName:     "main",
		calleeName:     "Register",
		callSiteFile:   "cmd/max-context/main.go",
		wantReceiver:   "Flags",
		wantTargetPkg:  "config",
		wantTargetFile: "internal/config/config.go",
		wantMarker:     "receiver-typed",
	},
	{
		desc:           "h.Register in register.go -> mcp.Handler.Register (not config.Flags.Register)",
		callerName:     "RegisterAll",
		calleeName:     "Register",
		callSiteFile:   "internal/tools/register.go",
		wantReceiver:   "Handler",
		wantTargetPkg:  "mcp",
		wantTargetFile: "internal/mcp/handler.go",
		wantMarker:     "receiver-typed",
	},
	{
		desc:           "setup.Run in main.go -> setup.Run cross-package (NOT bench.Run)",
		callerName:     "runSetup",
		calleeName:     "Run",
		callSiteFile:   "cmd/max-context/main.go",
		wantTargetPkg:  "setup",
		wantTargetFile: "internal/setup/setup.go",
		wantMarker:     "cross-package",
	},
	{
		desc:           "bench.Run in main.go -> bench.Run cross-package (NOT setup.Run)",
		callerName:     "runBench",
		calleeName:     "Run",
		callSiteFile:   "cmd/max-context/main.go",
		wantTargetPkg:  "bench",
		wantTargetFile: "internal/bench/runner.go",
		wantMarker:     "cross-package",
	},
}

// goldenAssert is the master switch. While false (Step 1, measure-first) the
// oracle only LOGS how each known-collision edge resolves today, documenting
// the baseline. Step 5 flips it to true to turn the oracle into a hard
// regression gate once the scope resolver lands.
const goldenAssert = true

// lookupEdge returns the stored edge matching (caller function name, callee
// name, call-site file) and its resolved target metadata. ok is false when no
// such edge is indexed.
type storedEdge struct {
	resolution string
	targetFile sql.NullString
	targetPkg  sql.NullString
	targetRecv sql.NullString
}

func lookupEdge(t *testing.T, database *sql.DB, g goldenEdge) (storedEdge, bool) {
	t.Helper()
	row := database.QueryRow(`
		SELECT e.resolution, tf.file_path, tf.package, tf.receiver_type
		FROM calls e
		JOIN functions caller ON caller.id = e.caller_id
		LEFT JOIN functions tf ON tf.id = e.callee_id
		WHERE caller.name = ? AND e.callee_name = ? AND e.file_path = ?
		LIMIT 1`,
		g.callerName, g.calleeName, g.callSiteFile,
	)
	var se storedEdge
	err := row.Scan(&se.resolution, &se.targetFile, &se.targetPkg, &se.targetRecv)
	if err == sql.ErrNoRows {
		return storedEdge{}, false
	}
	if err != nil {
		t.Fatalf("lookupEdge(%s): %v", g.desc, err)
	}
	return se, true
}

// TestResolutionGoldenEdges checks the hand-labeled collision edges. It is the
// precision regression gate (active once goldenAssert is true); until then it
// documents current resolution so the measure-first baseline is on record.
func TestResolutionGoldenEdges(t *testing.T) {
	database := indexSelfRepo(t)

	for _, g := range goldenEdges {
		se, ok := lookupEdge(t, database, g)
		if !ok {
			msg := "golden edge not indexed: " + g.desc
			if goldenAssert {
				t.Errorf("%s", msg)
			} else {
				t.Logf("[baseline] MISSING %s", msg)
			}
			continue
		}

		gotFile := se.targetFile.String
		if !se.targetFile.Valid {
			gotFile = "(unlinked)"
		}
		correct := se.resolution == g.wantMarker &&
			(g.wantTargetFile == "" || gotFile == g.wantTargetFile) &&
			(g.wantReceiver == "" || se.targetRecv.String == g.wantReceiver)

		if goldenAssert {
			if !correct {
				t.Errorf("%s\n  got: marker=%q target=%q recv=%q\n  want: marker=%q target=%q recv=%q",
					g.desc, se.resolution, gotFile, se.targetRecv.String,
					g.wantMarker, g.wantTargetFile, g.wantReceiver)
			}
			continue
		}
		t.Logf("[baseline] %s\n  now: marker=%q -> target=%q recv=%q  | want marker=%q target=%q",
			g.desc, se.resolution, gotFile, se.targetRecv.String, g.wantMarker, g.wantTargetFile)
	}
}

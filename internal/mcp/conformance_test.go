package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// serve runs the stdio loop over the given newline-delimited input and returns
// each response line decoded.
func serve(t *testing.T, input string) []map[string]interface{} {
	t.Helper()
	var out bytes.Buffer
	s := &Server{handler: NewHandler(), stdin: strings.NewReader(input), stdout: &out, schemas: nil}
	_ = s.Serve()

	var responses []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("response is not JSON: %q", line)
		}
		responses = append(responses, m)
	}
	return responses
}

const initReq = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`

// JSON-RPC 2.0 forbids replying to a notification, and MCP sends the
// post-handshake ack as "notifications/initialized". The server used to answer
// it with -32601 method-not-found, so strict clients saw an error at startup on
// the single most-executed path in the product.
func TestNotificationsGetNoResponse(t *testing.T) {
	for _, method := range []string{
		"notifications/initialized",
		"notifications/cancelled",
		"notifications/progress",
	} {
		t.Run(method, func(t *testing.T) {
			in := initReq + "\n" + `{"jsonrpc":"2.0","method":"` + method + `"}` + "\n"
			responses := serve(t, in)
			if len(responses) != 1 {
				t.Fatalf("expected only the initialize response, got %d:\n%v", len(responses), responses)
			}
			if responses[0]["id"] != float64(1) {
				t.Errorf("the one response should be for initialize, got %v", responses[0])
			}
		})
	}
}

// Any request without an id is a notification, whatever the method name.
func TestRequestWithoutIDGetsNoResponse(t *testing.T) {
	responses := serve(t, `{"jsonrpc":"2.0","method":"tools/list"}`+"\n")
	if len(responses) != 0 {
		t.Errorf("id-less request must not be answered, got %v", responses)
	}
}

// A method that genuinely does not exist still gets an error — as long as it
// was a request, not a notification.
func TestUnknownMethodStillErrors(t *testing.T) {
	responses := serve(t, `{"jsonrpc":"2.0","id":7,"method":"no/such/method"}`+"\n")
	if len(responses) != 1 {
		t.Fatalf("expected one error response, got %v", responses)
	}
	errObj, ok := responses[0]["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected an error object, got %v", responses[0])
	}
	if errObj["code"] != float64(CodeMethodNotFound) {
		t.Errorf("expected %d, got %v", CodeMethodNotFound, errObj["code"])
	}
}

// A single large message used to kill the whole server: bufio.Scanner defaults
// to a 64 KiB line and returns ErrTooLong past it, so the process exited
// mid-session and every later request went unanswered.
func TestLargeRequestDoesNotKillServer(t *testing.T) {
	big := strings.Repeat("x", 200*1024)
	payload, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]interface{}{
			"name":      "get_definition",
			"arguments": map[string]interface{}{"symbol": big},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	in := initReq + "\n" + string(payload) + "\n" + `{"jsonrpc":"2.0","id":3,"method":"ping"}` + "\n"

	responses := serve(t, in)
	if len(responses) != 3 {
		t.Fatalf("expected 3 responses (init, oversized call, ping), got %d:\n%v", len(responses), responses)
	}
	if responses[2]["id"] != float64(3) {
		t.Errorf("server stopped before the request after the large one: %v", responses)
	}
}

// Protocol negotiation: echo what the client asked for when supported,
// otherwise answer with the newest version we speak.
func TestProtocolVersionNegotiation(t *testing.T) {
	for _, tc := range []struct{ requested, want string }{
		{"2025-06-18", "2025-06-18"},
		{"2024-11-05", "2024-11-05"},
		{"1999-01-01", SupportedProtocolVersions[0]},
		{"", SupportedProtocolVersions[0]},
	} {
		if got := negotiateProtocolVersion(tc.requested); got != tc.want {
			t.Errorf("negotiate(%q) = %q, want %q", tc.requested, got, tc.want)
		}
	}
}

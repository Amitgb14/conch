package client

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

const (
	testUserToken = "conch_t_0011aabb_22334455_" + "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	testPaneToken = "conch_p_0011aabb_22334455_" + "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
)

// tokenServer is a fake that answers auth.token, recording what it was
// given, and refuses refuse.
func tokenServer(t *testing.T, caps []string, refuse bool) (*a3Server, *[]string) {
	var mu sync.Mutex
	var got []string
	s := a3NewServer(t, func(conn *proto.Conn, msg proto.Message) {
		if msg.Method != proto.MethodAuthToken {
			a3Reply(conn, msg, nil, proto.Errorf(proto.ErrUnknown, "no %s", msg.Method))
			return
		}
		var p proto.AuthTokenParams
		_ = json.Unmarshal(msg.Params, &p)
		mu.Lock()
		got = append(got, p.Token)
		mu.Unlock()
		if refuse {
			a3Reply(conn, msg, nil, proto.Errorf(proto.ErrUnauthorized, "that token is unknown or revoked"))
			return
		}
		a3Reply(conn, msg, proto.AuthTokenResult{Name: "ci", Level: proto.TokenRead}, nil)
	})
	s.caps = caps
	return s, &got
}

// The token in CONCH_TOKEN is presented right after the handshake, and
// what it holds the connection to is kept.
func TestDialPresentsToken(t *testing.T) {
	s, got := tokenServer(t, []string{"pane.v1", proto.CapToken}, false)
	t.Setenv("CONCH_TOKEN", " "+testUserToken+"\n") // as a shell's $(cat file) leaves it
	c := s.dial(t)
	if len(*got) != 1 || (*got)[0] != testUserToken {
		t.Fatalf("presented %q", *got)
	}
	if c.Token.Name != "ci" || c.Token.Level != proto.TokenRead {
		t.Errorf("token result %+v", c.Token)
	}
}

// No token, nothing presented: the person's own shell is unchanged.
func TestDialWithoutTokenPresentsNothing(t *testing.T) {
	s, got := tokenServer(t, []string{"pane.v1", proto.CapToken}, false)
	c := s.dial(t)
	if len(*got) != 0 || c.Token.Name != "" {
		t.Errorf("presented %q, holds %+v", *got, c.Token)
	}
}

// A refused token is a failed connection, never one that carries on as
// the person.
func TestDialRefusedTokenFails(t *testing.T) {
	s, _ := tokenServer(t, []string{"pane.v1", proto.CapToken}, true)
	t.Setenv("CONCH_TOKEN", testUserToken)
	c, err := Dial(s.sock, "test")
	if err == nil {
		c.Close()
		t.Fatal("a refused token connected")
	}
	if !strings.Contains(err.Error(), "refused") || !strings.Contains(err.Error(), "CONCH_TOKEN") {
		t.Errorf("error %q", err)
	}
}

// A server without token.v1 cannot check a token the person made, so the
// client says so rather than act as the person; a pane's own token is
// let go, since that server still scopes the pane by its process tree.
func TestDialOlderServerWithoutTokens(t *testing.T) {
	s, got := tokenServer(t, []string{"pane.v1"}, false)
	t.Setenv("CONCH_TOKEN", testUserToken)
	if c, err := Dial(s.sock, "test"); err == nil {
		c.Close()
		t.Fatal("a person's token went to a server that cannot check it")
	} else if !strings.Contains(err.Error(), "older build") {
		t.Errorf("error %q", err)
	}
	t.Setenv("CONCH_TOKEN", testPaneToken)
	c, err := Dial(s.sock, "test")
	if err != nil {
		t.Fatalf("a pane token kept a client off an older server: %v", err)
	}
	c.Close()
	if len(*got) != 0 {
		t.Errorf("auth.token sent to a server without it: %q", *got)
	}
}

package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAgree(t *testing.T) {
	if _, err := agree([]time.Duration{0}); err == nil {
		t.Fatal("one server must not be enough")
	}
	if _, err := agree([]time.Duration{0, time.Hour}); err == nil {
		t.Fatal("two servers an hour apart must not agree")
	}
	got, err := agree([]time.Duration{time.Hour, 0, 2 * time.Second})
	if err != nil || got != 2*time.Second {
		t.Fatalf("got %v %v, want the median", got, err)
	}
}

// fakeServer simulates asking a server whose clock is ahead by ahead: the
// request goes out at sent on the local clock, the server stamps Date
// (truncated to the second) after up, and the reply's first byte is back
// after down more.
func fakeServer(ahead time.Duration, sent time.Time, up, down time.Duration) (date, recv time.Time) {
	date = sent.Add(up + ahead).Truncate(time.Second)
	return date, sent.Add(up + down)
}

func TestOffset(t *testing.T) {
	local := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		ahead    time.Duration
		at       time.Duration // when the request goes out, after local
		up, down time.Duration
	}{
		{"fast link", 3 * time.Hour, 0, 20 * time.Millisecond, 20 * time.Millisecond},
		{"slow link", -40 * time.Minute, 0, 900 * time.Millisecond, 900 * time.Millisecond},
		{"lopsided", 7 * time.Second, 0, 50 * time.Millisecond, 1200 * time.Millisecond},
		{"asked late", 3 * time.Hour, 13 * time.Second, 30 * time.Millisecond, 30 * time.Millisecond},
		{"mid-second", 250 * time.Millisecond, 600 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond},
	} {
		sent := local.Add(tc.at)
		date, recv := fakeServer(tc.ahead, sent, tc.up, tc.down)
		got := offset(date, sent, recv)
		// Date's truncation costs up to half a second either way; an
		// uneven trip costs up to half the difference between its legs.
		tol := 500*time.Millisecond + (tc.up-tc.down).Abs()/2
		if (got - tc.ahead).Abs() > tol {
			t.Errorf("%s: offset %v, want %v ± %v", tc.name, got, tc.ahead, tol)
		}
	}
}

// Servers asked one after another, with a 5 s timeout in between, still
// agree, and the clock is set from the offset when it is set, not from a
// time that was right a few seconds earlier.
func TestOffsetsFromSlowRound(t *testing.T) {
	local := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	const ahead = 2*time.Hour + 300*time.Millisecond
	var offs []time.Duration
	at := local
	for _, wait := range []time.Duration{0, 5 * time.Second, 8 * time.Second} { // a timeout, then a slow server
		at = at.Add(wait)
		date, recv := fakeServer(ahead, at, 40*time.Millisecond, 40*time.Millisecond)
		offs = append(offs, offset(date, at, recv))
		at = recv
	}
	off, err := agree(offs)
	if err != nil {
		t.Fatalf("%v: %v", offs, err)
	}
	setAt := at.Add(1500 * time.Millisecond) // the time the clock is set
	if d := setAt.Add(off).Sub(setAt.Add(ahead)); d.Abs() > 500*time.Millisecond {
		t.Errorf("clock set %v from the right time", d)
	}
}

// The round trip runs from the request to the first byte of the reply: a
// HEAD reply with a Content-Length but no body is read at once, and the
// connection staying open afterwards adds nothing.
func TestQueryHEAD(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	stamp := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	go func() {
		req, err := http.ReadRequest(bufio.NewReader(server))
		if err != nil || req.Method != http.MethodHead || req.Host != "example.com" {
			t.Errorf("request: %+v %v", req, err)
			return
		}
		time.Sleep(50 * time.Millisecond)
		fmt.Fprintf(server, "HTTP/1.1 200 OK\r\nDate: %s\r\nContent-Length: 1000\r\n\r\n", stamp.Format(http.TimeFormat))
		// no body, and the connection stays open
	}()
	client.SetDeadline(time.Now().Add(3 * time.Second))
	start := time.Now()
	date, sent, recv, err := query(client, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("query took %v: it waited for a body", took)
	}
	if !date.Equal(stamp) {
		t.Errorf("date %v, want %v", date, stamp)
	}
	if rtt := recv.Sub(sent); rtt < 50*time.Millisecond || rtt > 500*time.Millisecond {
		t.Errorf("round trip %v, want about 50 ms", rtt)
	}
}

func TestQueryCapsReply(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		http.ReadRequest(bufio.NewReader(server))
		fmt.Fprint(server, "HTTP/1.1 200 OK\r\n")
		line := []byte("X-Pad: " + strings.Repeat("a", 1000) + "\r\n")
		for {
			if _, err := server.Write(line); err != nil {
				return
			}
		}
	}()
	client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, _, _, err := query(client, "example.com"); err == nil {
		t.Fatal("endless headers must fail")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("read until the deadline (%v): the reply isn't capped", err)
	}
}

func TestVerifyFloor(t *testing.T) {
	if err := verify(nil, "x", floor.Add(time.Hour)); err == nil {
		t.Fatal("no certificates must fail")
	}
}

func TestOnionOffset(t *testing.T) {
	for in, want := range map[int]string{-5 * 3600: "UTC+05:00", -4 * 3600: "UTC+04:00", 0: "UTC+00:00", 5*3600 + 1800: "UTC-05:30"} {
		if got := onionOffset(in); got != want {
			t.Errorf("%d: got %s want %s", in, got, want)
		}
	}
}

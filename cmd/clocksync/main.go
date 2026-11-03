// clocksync sets the Miyoo's clock at boot from the Date header of a few
// well-known HTTPS sites. Onion's own sync waits on worldtimeapi.org, which
// no longer answers, so the clock drifts (and MiniKey's codes need it right).
//
// The time is only trusted from a server whose certificate chain verifies
// for its name. The chain is checked at the time the server reports (the
// clock may be far off, which would make every certificate look invalid),
// and that time must be later than this program's build floor. Several
// servers must agree within a few seconds.
//
//	clocksync           wait for the network (up to 60 s), then sync once
//	clocksync -n        dry run: print what it would do
package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // the card has no zoneinfo

	_ "golang.org/x/crypto/x509roots/fallback" // the card has no CA bundle

	"github.com/zakijss/minideck/atomicfile"
	"github.com/zakijss/minideck/version"
)

// floor: no correct time can be earlier than this program.
var floor = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

// hosts are asked in turn; the first three that answer are compared.
var hosts = []string{"www.cloudflare.com", "www.google.com", "github.com", "www.apple.com"}

func main() {
	version.Flag("clocksync")
	version.Note("clocksync")
	dry := flag.Bool("n", false, "dry run")
	wait := flag.Duration("wait", 60*time.Second, "how long to wait for the network")
	zoneFile := flag.String("zonefile", "/mnt/SDCARD/App/MiniTop/timezone", "file naming the time zone (e.g. Europe/Berlin); Onion's offset follows it, daylight saving included")
	flag.Parse()
	setZone(*zoneFile, *dry) // needs no network: do it first

	deadline := time.Now().Add(*wait)
	var offs []time.Duration
	for {
		offs = offs[:0]
		for _, h := range hosts {
			off, err := serverOffset(h)
			if err != nil {
				log.Printf("%s: %v", h, err)
				continue
			}
			offs = append(offs, off)
			if len(offs) == 3 {
				break
			}
		}
		if len(offs) >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Second)
	}
	off, err := agree(offs)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("servers say %s (clock off by %.1f s)", time.Now().Add(off).UTC().Format(time.RFC3339), off.Seconds())
	if *dry {
		return
	}
	if off.Abs() >= 2*time.Second {
		// The offset is applied to the clock as it reads now, so the time
		// spent asking the servers isn't lost.
		tv := syscall.NsecToTimeval(time.Now().Add(off).UnixNano())
		if err := syscall.Settimeofday(&tv); err != nil {
			log.Fatalf("set clock: %v", err)
		}
		if err := exec.Command("hwclock", "-w").Run(); err != nil { // as Onion keeps it
			log.Printf("hwclock: %v", err)
		}
		log.Printf("clock set")
	}
	os.WriteFile("/tmp/ntp_synced", nil, 0644) // Onion's "time synced" marker
}

// Onion keeps a fixed offset in .tz ("UTC+05:00" means five hours behind
// UTC, the POSIX way round) and its own zone lookup no longer works, so the
// offset never changes for daylight saving. With a zone named in zoneFile,
// write the zone's current offset there at every boot.
const onionTZ = "/mnt/SDCARD/.tmp_update/config/.tz"

// setZone writes the current offset of the zone named in zoneFile to
// Onion's .tz, when it differs.
func setZone(zoneFile string, dry bool) {
	b, err := os.ReadFile(zoneFile)
	if err != nil {
		return // not set up: leave Onion's setting alone
	}
	name := strings.TrimSpace(string(b))
	loc, err := time.LoadLocation(name)
	if err != nil {
		log.Printf("time zone %q: %v", name, err)
		return
	}
	_, off := time.Now().In(loc).Zone()
	want := onionOffset(off)
	cur, _ := os.ReadFile(onionTZ)
	if strings.TrimSpace(string(cur)) == want {
		return
	}
	log.Printf("time zone %s: Onion offset %s -> %s", name, strings.TrimSpace(string(cur)), want)
	if dry {
		return
	}
	for _, f := range []string{onionTZ, onionTZ + "_sync"} {
		if err := atomicfile.Write(f, []byte(want+"\n"), 0644); err != nil {
			log.Printf("time zone: %v", err) // e.g. a full card: Onion's file is left as it was
		}
	}
}

// onionOffset formats seconds east of UTC the way Onion stores them.
func onionOffset(sec int) string {
	sign := "-"
	if sec <= 0 {
		sign, sec = "+", -sec
	}
	return fmt.Sprintf("UTC%s%02d:%02d", sign, sec/3600, sec%3600/60)
}

// serverOffset returns how far a server's clock is ahead of this one, if
// its certificate verifies for the time it reports.
func serverOffset(host string) (time.Duration, error) {
	d := &net.Dialer{Timeout: 5 * time.Second}
	// The handshake happens here, before the round trip is timed. The chain
	// is verified below, at the time the server reports.
	c, err := tls.DialWithDialer(d, "tcp", host+":443", &tls.Config{ServerName: host, InsecureSkipVerify: true})
	if err != nil {
		return 0, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(8 * time.Second))
	date, sent, recv, err := query(c, host)
	if err != nil {
		return 0, err
	}
	if err := verify(c.ConnectionState().PeerCertificates, host, date); err != nil {
		return 0, err
	}
	return offset(date, sent, recv), nil
}

// query sends a HEAD request over c and returns the reply's Date with the
// local times the request was sent and the first byte of the reply came
// back. Only that span is the round trip: the handshake before it and
// reading the rest of the reply after it would make it look longer.
func query(c net.Conn, host string) (date, sent, recv time.Time, err error) {
	req, err := http.NewRequest(http.MethodHead, "https://"+host+"/", nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", "clocksync")
	req.Close = true
	var out bytes.Buffer
	if err = req.Write(&out); err != nil {
		return
	}
	// The reply is read before the chain is verified, so cap it: a
	// stranger's endless headers mustn't fill the device's memory.
	br := bufio.NewReader(io.LimitReader(c, maxReply))
	sent = time.Now()
	if _, err = c.Write(out.Bytes()); err != nil {
		return
	}
	if _, err = br.Peek(1); err != nil {
		return
	}
	recv = time.Now()
	// With the request given, a HEAD reply is read without a body, so
	// nothing waits for one that never comes.
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return
	}
	resp.Body.Close()
	date, err = http.ParseTime(resp.Header.Get("Date"))
	if err != nil {
		err = errors.New("no Date header")
	}
	return
}

// maxReply caps the bytes read of a server's reply (its headers).
const maxReply = 64 << 10

// offset is how far the server's clock is ahead of the local one, given
// the server's Date and the local times the request went out and the reply
// started to arrive. The server stamped Date somewhere in that span, and
// Date is truncated to the second, so the middle of both is the best guess.
func offset(date, sent, recv time.Time) time.Duration {
	mid := sent.Add(recv.Sub(sent) / 2)
	return date.Add(500 * time.Millisecond).Sub(mid)
}

// verify checks the server's chain for host at time at, which must not be
// before floor.
func verify(certs []*x509.Certificate, host string, at time.Time) error {
	if len(certs) == 0 {
		return errors.New("no certificate")
	}
	if at.Before(floor) {
		return fmt.Errorf("server time %s is before %s", at.Format(time.DateOnly), floor.Format(time.DateOnly))
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	_, err := certs[0].Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter, CurrentTime: at})
	return err
}

// agree returns the median offset if at least two servers agree with it
// within 5 s. Offsets, unlike the times the servers report, don't depend on
// when each server was asked, so a slow server in between doesn't make the
// others look wrong.
func agree(offs []time.Duration) (time.Duration, error) {
	if len(offs) < 2 {
		return 0, fmt.Errorf("only %d server(s) answered: clock not changed", len(offs))
	}
	s := slices.Clone(offs)
	slices.Sort(s)
	m := s[len(s)/2]
	n := 0
	for _, o := range s {
		if (o - m).Abs() <= 5*time.Second {
			n++
		}
	}
	if n < 2 {
		return 0, errors.New("servers disagree: clock not changed")
	}
	return m, nil
}

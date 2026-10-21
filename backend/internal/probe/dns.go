package probe

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"argus/internal/store"
)

// DNSRecordTypes are the record types a dns monitor can ask for.
// SOA, SRV and CAA are absent because Go's resolver cannot ask for them, and
// answering with something else would be a lie. They need a DNS library.
var DNSRecordTypes = []string{"A", "AAAA", "CNAME", "MX", "NS", "TXT"}

// ValidDNSRecord reports whether rec is a supported record type.
func ValidDNSRecord(rec string) bool {
	rec = strings.ToUpper(strings.TrimSpace(rec))
	for _, r := range DNSRecordTypes {
		if r == rec {
			return true
		}
	}
	return false
}

// normalizeAnswer makes answers comparable: hostnames lose their trailing dot
// and case, everything else is only trimmed.
func normalizeAnswer(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

func checkDNS(ctx context.Context, m store.Monitor) (CheckOutcome, error) {
	timeout := time.Duration(timeoutSeconds(m, 5)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	want := strings.ToUpper(strings.TrimSpace(m.Config.Record))
	if want == "" {
		want = "A"
	}
	r := net.Resolver{}
	start := time.Now()
	answers, err := lookupRecords(ctx, &r, want, m.Target)
	out := CheckOutcome{LatencyMs: float64(time.Since(start).Microseconds()) / 1000}
	out.Evidence.DNSAnswers = answers
	if err != nil {
		return out, err
	}
	if len(answers) == 0 {
		return out, fmt.Errorf("no %s records", want)
	}

	// Expected value: expect_ip for addresses, expect_value for the rest. A TXT
	// record matches on substring, because SPF and verification strings are
	// long and operators care about one fragment.
	expect := strings.TrimSpace(m.Config.ExpectValue)
	if expect == "" {
		expect = strings.TrimSpace(m.Config.ExpectIP)
	}
	if expect == "" {
		return out, nil
	}
	if want == "A" || want == "AAAA" {
		wantIP := net.ParseIP(expect)
		for _, a := range answers {
			if wantIP != nil && net.ParseIP(a).Equal(wantIP) {
				return out, nil
			}
		}
		return out, fmt.Errorf("expected %s not in %s answers (%s)", expect, want, strings.Join(answers, ", "))
	}
	needle := normalizeAnswer(expect)
	for _, a := range answers {
		got := normalizeAnswer(a)
		if got == needle || (want == "TXT" && strings.Contains(got, needle)) {
			return out, nil
		}
	}
	return out, fmt.Errorf("expected %q not in %s answers (%s)", expect, want, strings.Join(answers, ", "))
}

// lookupRecords asks the resolver for one record type and returns the answers
// as text, in a stable order so evidence does not churn between checks.
func lookupRecords(ctx context.Context, r *net.Resolver, record, target string) ([]string, error) {
	host := strings.TrimSuffix(strings.TrimSpace(target), ".")
	var out []string
	switch record {
	case "A", "AAAA":
		addrs, err := r.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			isV4 := a.IP.To4() != nil
			if (record == "A") == isV4 {
				out = append(out, a.IP.String())
			}
		}
	case "CNAME":
		cname, err := r.LookupCNAME(ctx, host)
		if err != nil {
			return nil, err
		}
		// The resolver echoes the name back when there is no CNAME.
		if normalizeAnswer(cname) != normalizeAnswer(host) {
			out = append(out, cname)
		}
	case "MX":
		mxs, err := r.LookupMX(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, mx := range mxs {
			out = append(out, fmt.Sprintf("%d %s", mx.Pref, strings.TrimSuffix(mx.Host, ".")))
		}
	case "NS":
		nss, err := r.LookupNS(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ns := range nss {
			out = append(out, strings.TrimSuffix(ns.Host, "."))
		}
	case "TXT":
		txts, err := r.LookupTXT(ctx, host)
		if err != nil {
			return nil, err
		}
		out = append(out, txts...)
	default:
		return nil, fmt.Errorf("unsupported dns record %q", record)
	}
	sort.Strings(out)
	return out, nil
}

package api

import "testing"

func TestMapKumaMonitor(t *testing.T) {
	httpM, _, err := mapKumaMonitor(kumaMonitor{
		Name: "site", Type: "http", URL: "https://example.com/health", Interval: 60, Method: "GET",
		Keyword: "ok", InvertKeyword: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if httpM.Type != "http" || httpM.Target != "https://example.com/health" || !httpM.Config.KeywordInvert {
		t.Fatalf("%#v", httpM)
	}

	ping, _, err := mapKumaMonitor(kumaMonitor{Name: "gw", Type: "ping", Hostname: "10.0.0.1"})
	if err != nil || ping.Type != "icmp" || ping.Target != "10.0.0.1" {
		t.Fatalf("%v %#v", err, ping)
	}

	port, _, err := mapKumaMonitor(kumaMonitor{Name: "db", Type: "port", Hostname: "db.internal", Port: 5432})
	if err != nil || port.Type != "tcp" || port.Target != "db.internal:5432" {
		t.Fatalf("%v %#v", err, port)
	}

	_, _, err = mapKumaMonitor(kumaMonitor{Name: "box", Type: "docker", Hostname: "host"})
	if err == nil {
		t.Fatal("expected docker skip")
	}
}

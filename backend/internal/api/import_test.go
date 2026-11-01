package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"argus/internal/store"
)

func (h *apiHarness) channel(body string) store.Channel {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/channels", body)
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("create channel %d %s", rec.Code, rec.Body.String())
	}
	var c store.Channel
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		h.t.Fatal(err)
	}
	return c
}

func (h *apiHarness) monitorWithChannels(name, channelIDs string) {
	h.t.Helper()
	body := fmt.Sprintf(`{"name":%q,"type":"http","target":"https://%s.example.com","interval_seconds":60,"channel_ids":%s}`, name, name, channelIDs)
	if rec := h.do(http.MethodPost, "/api/monitors", body); rec.Code != http.StatusCreated {
		h.t.Fatalf("create monitor %d %s", rec.Code, rec.Body.String())
	}
}

func (h *apiHarness) exported() string {
	h.t.Helper()
	rec := h.do(http.MethodGet, "/api/export", "")
	if rec.Code != http.StatusOK {
		h.t.Fatalf("export %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

type importResult struct {
	Imported int                 `json:"imported"`
	Channels int                 `json:"channels"`
	Skipped  []map[string]string `json:"skipped"`
}

func (h *apiHarness) importExport(export string) importResult {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/import", fmt.Sprintf(`{"format":"argus","data":%s}`, export))
	if rec.Code != http.StatusOK {
		h.t.Fatalf("import %d %s", rec.Code, rec.Body.String())
	}
	var res importResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		h.t.Fatal(err)
	}
	return res
}

func (h *apiHarness) channelsByName() map[string]store.Channel {
	h.t.Helper()
	var list []store.Channel
	if err := json.Unmarshal(h.do(http.MethodGet, "/api/channels", "").Body.Bytes(), &list); err != nil {
		h.t.Fatal(err)
	}
	out := map[string]store.Channel{}
	for _, c := range list {
		out[c.Name] = c
	}
	return out
}

func (h *apiHarness) monitorChannels() map[string][]int64 {
	h.t.Helper()
	var list []store.Monitor
	if err := json.Unmarshal(h.do(http.MethodGet, "/api/monitors", "").Body.Bytes(), &list); err != nil {
		h.t.Fatal(err)
	}
	out := map[string][]int64{}
	for _, m := range list {
		out[m.Name] = m.ChannelIDs
	}
	return out
}

func TestImportRestoresChannelsAndAssignments(t *testing.T) {
	src := newAPIHarness(t)
	ops := src.channel(`{"name":"ops","type":"webhook","dest":"https://hooks.example.com/ops","events":["down","recovery"]}`)
	pager := src.channel(`{"name":"pager","type":"pagerduty","dest":"routing-key-1234"}`)
	src.monitorWithChannels("both", fmt.Sprintf(`[%d,%d]`, ops.ID, pager.ID))
	src.monitorWithChannels("pager-only", fmt.Sprintf(`[%d]`, pager.ID))
	src.monitorWithChannels("default", `null`)
	src.monitorWithChannels("silent", `[]`)
	export := src.exported()

	// A channel already here pushes the new ids away from the exported ones,
	// so an assignment that kept the old ids would point at the wrong channel.
	dst := newAPIHarness(t)
	dst.channel(`{"name":"already-here","type":"webhook","dest":"https://hooks.example.com/here"}`)

	res := dst.importExport(export)
	if res.Imported != 4 || res.Channels != 2 || len(res.Skipped) != 0 {
		t.Fatalf("result %+v", res)
	}
	chs := dst.channelsByName()
	if len(chs) != 3 {
		t.Fatalf("channels %+v", chs)
	}
	got := chs["ops"]
	if got.Type != "webhook" || got.Dest != ops.Dest || !slices.Equal(got.Events, ops.Events) {
		t.Fatalf("ops restored as %+v", got)
	}
	if chs["pager"].Dest != pager.Dest || len(chs["pager"].Events) != len(pager.Events) {
		t.Fatalf("pager restored as %+v", chs["pager"])
	}
	if chs["pager"].ID == pager.ID {
		t.Fatalf("fixture did not move the ids, so this proves nothing")
	}

	mons := dst.monitorChannels()
	if !slices.Equal(mons["both"], []int64{chs["ops"].ID, chs["pager"].ID}) {
		t.Fatalf("both -> %v", mons["both"])
	}
	if !slices.Equal(mons["pager-only"], []int64{chs["pager"].ID}) {
		t.Fatalf("pager-only -> %v", mons["pager-only"])
	}
	// nil is every channel, empty is none. They alert different people.
	if mons["default"] != nil {
		t.Fatalf("default -> %v", mons["default"])
	}
	if mons["silent"] == nil || len(mons["silent"]) != 0 {
		t.Fatalf("silent -> %#v", mons["silent"])
	}
}

// Restoring the same export twice must not make every alert go out twice.
func TestImportReusesAChannelItAlreadyHas(t *testing.T) {
	h := newAPIHarness(t)
	c := h.channel(`{"name":"ops","type":"webhook","dest":"https://hooks.example.com/ops"}`)
	h.channel(`{"name":"ops-urgent","type":"webhook","dest":"https://hooks.example.com/ops","events":["down"]}`)
	h.monitorWithChannels("shop", fmt.Sprintf(`[%d]`, c.ID))

	res := h.importExport(h.exported())
	if res.Channels != 0 || res.Imported != 1 || len(res.Skipped) != 0 {
		t.Fatalf("result %+v", res)
	}
	if chs := h.channelsByName(); len(chs) != 2 {
		t.Fatalf("channels %+v", chs)
	}
	// Importing copies the monitor, so check the original and the copy.
	var list []store.Monitor
	if err := json.Unmarshal(h.do(http.MethodGet, "/api/monitors", "").Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("%d monitors", len(list))
	}
	for _, m := range list {
		if !slices.Equal(m.ChannelIDs, []int64{c.ID}) {
			t.Fatalf("monitor %d -> %v", m.ID, m.ChannelIDs)
		}
	}
}

// An export can be edited by hand, so a channel passes the checks the form
// applies. A monitor left with no channel falls back to every channel, as it
// does when its last channel is deleted: alerting nobody is the worse failure.
func TestImportSkipsAChannelThatFailsValidation(t *testing.T) {
	h := newAPIHarness(t)
	export := `{"format":"argus","channels":[
		{"id":7,"name":"ops","type":"webhook","dest":"https://hooks.example.com/ops","events":["down"]},
		{"id":8,"name":"bad","type":"webhook","dest":"ftp://secret-token@hooks.example.com/","events":["down"]},
		{"id":9,"name":"unknown-event","type":"slack","dest":"https://hooks.example.com/slack","events":["nonsense"]}
	],"monitors":[
		{"id":1,"name":"kept","type":"http","target":"https://kept.example.com","interval_seconds":60,"enabled":true,"channel_ids":[7,8]},
		{"id":2,"name":"lost","type":"http","target":"https://lost.example.com","interval_seconds":60,"enabled":true,"channel_ids":[8]},
		{"id":3,"name":"dangling","type":"http","target":"https://dangling.example.com","interval_seconds":60,"enabled":true,"channel_ids":[99]}
	]}`
	res := h.importExport(export)
	if res.Imported != 3 || res.Channels != 1 || len(res.Skipped) != 2 {
		t.Fatalf("result %+v", res)
	}
	if raw, _ := json.Marshal(res.Skipped); strings.Contains(string(raw), "secret-token") {
		t.Fatalf("skip reason repeats the destination: %s", raw)
	}
	chs := h.channelsByName()
	if len(chs) != 1 {
		t.Fatalf("channels %+v", chs)
	}
	mons := h.monitorChannels()
	if !slices.Equal(mons["kept"], []int64{chs["ops"].ID}) {
		t.Fatalf("kept -> %v", mons["kept"])
	}
	if mons["lost"] != nil || mons["dangling"] != nil {
		t.Fatalf("lost -> %v, dangling -> %v", mons["lost"], mons["dangling"])
	}
}

func TestImportKeepsToTheChannelLimit(t *testing.T) {
	h := newAPIHarness(t)
	var chs []string
	for i := 1; i <= maxChannels+2; i++ {
		chs = append(chs, fmt.Sprintf(`{"id":%d,"name":"c%d","type":"webhook","dest":"https://hooks.example.com/%d","events":["down"]}`, i, i, i))
	}
	res := h.importExport(fmt.Sprintf(`{"format":"argus","channels":[%s],"monitors":[]}`, strings.Join(chs, ",")))
	if res.Channels != maxChannels || len(res.Skipped) != 2 {
		t.Fatalf("restored %d, skipped %+v", res.Channels, res.Skipped)
	}
}

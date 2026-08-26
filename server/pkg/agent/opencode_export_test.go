package agent

import "testing"

func TestOpencodeExportParse(t *testing.T) {
	data := []byte(`{"messages":[
{"info":{"role":"assistant","providerID":"old-provider","modelID":"old-model","tokens":{"input":1,"output":2}},"parts":[{"type":"text","text":"old"}]},
{"info":{"role":"user"},"parts":[{"type":"text","text":"question"}]},
{"info":{"role":"assistant","providerID":"provider","modelID":"model","tokens":{"input":4,"output":7,"reasoning":3,"cache":{"read":5,"write":6}}},"parts":[{"type":"reasoning","text":" latest reasoning "}]}
]}`)
	got, err := parseOpencodeExport(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.output != "latest reasoning" {
		t.Fatalf("output = %q", got.output)
	}
	if got.providerID != "provider" || got.modelID != "model" {
		t.Fatalf("model = %s/%s", got.providerID, got.modelID)
	}
	if got.usage.InputTokens != 4 || got.usage.OutputTokens != 7 || got.usage.CacheReadTokens != 5 || got.usage.CacheWriteTokens != 6 {
		t.Fatalf("usage = %+v", got.usage)
	}
}

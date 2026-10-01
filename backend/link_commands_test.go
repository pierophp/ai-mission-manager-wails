package backend

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestLinkAttentionDeletionCommandsDispatchAndRejectStalePreview(t *testing.T) {
	rt, err := OpenRuntime(t.TempDir() + "/links.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	service := CommandService{Runtime: rt}
	if _, err = service.Invoke("create_item", `{"title":"Track","contextId":1,"projectId":1,"notes":""}`); err != nil {
		t.Fatal(err)
	}
	linked, err := service.Invoke("link_external_object", `{"itemId":1,"url":"https://example.com/work/one"}`)
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		View struct {
			Link struct {
				ID int64 `json:"id"`
			} `json:"link"`
		} `json:"link"`
	}
	if err = json.Unmarshal(linked, &view); err != nil {
		t.Fatal(err)
	}
	if view.View.Link.ID == 0 {
		t.Fatalf("link result = %s", linked)
	}
	linkID := view.View.Link.ID
	policy := fmt.Sprintf(`{"linkId":%d,"policy":{"title_attention":false,"state_attention":null,"metadata_attention":false}}`, linkID)
	if _, err = service.Invoke("set_link_attention_policy", policy); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Invoke("set_link_review_at", fmt.Sprintf(`{"linkId":%d,"reviewAt":"2026-10-01T09:00"}`, linkID)); err != nil {
		t.Fatal(err)
	}
	previewRaw, err := service.Invoke("prepare_external_object_deletion", `{"externalObjectId":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var preview struct {
		StateFingerprint string `json:"state_fingerprint"`
	}
	if err = json.Unmarshal(previewRaw, &preview); err != nil {
		t.Fatal(err)
	}
	if preview.StateFingerprint == "" {
		t.Fatalf("preview omitted fingerprint: %s", previewRaw)
	}
	if _, err = service.Invoke("clear_link_review_at", fmt.Sprintf(`{"linkId":%d}`, linkID)); err != nil {
		t.Fatal(err)
	}
	stale := fmt.Sprintf(`{"externalObjectId":1,"confirmed":true,"stateFingerprint":%q}`, preview.StateFingerprint)
	if _, err = service.Invoke("delete_external_object", stale); err == nil {
		t.Fatal("stale deletion preview succeeded")
	}
	previewRaw, err = service.Invoke("prepare_external_object_deletion", `{"externalObjectId":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(previewRaw, &preview); err != nil {
		t.Fatal(err)
	}
	confirmed := fmt.Sprintf(`{"externalObjectId":1,"confirmed":true,"stateFingerprint":%q}`, preview.StateFingerprint)
	if _, err = service.Invoke("delete_external_object", confirmed); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Invoke("mark_link_reviewed", fmt.Sprintf(`{"linkId":%d}`, linkID)); err == nil {
		t.Fatal("command accepted a deleted Link")
	}
}

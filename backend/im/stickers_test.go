package im

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestStickerCollectionIsPrivateAndPersistent(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	host := call(t, s, "POST", "/guest", "", `{}`, 200)
	tok := host["token"].(string)
	f := call(t, s, "POST", "/stickers", tok, `{"name":"smile.png","size":8,"mime":"image/png"}`, 200)
	id := f["id"].(string)
	call(t, s, "PUT", "/stickers/"+id+"/chunks", tok, `{"offset":0,"dataBase64":"`+base64.StdEncoding.EncodeToString([]byte("testdata"))+`"}`, 200)
	call(t, s, "POST", "/stickers/"+id+"/finish", tok, `{}`, 200)
	s.Close()
	s, _ = New(dir)
	list := call(t, s, "GET", "/stickers", tok, ``, 200)
	if len(list["stickers"].([]any)) != 1 {
		t.Fatal(list)
	}
	_, v := s.request("POST", "/guest", "", json.RawMessage(`{"nickname":"other"}`), false)
	other := v.(map[string]any)["token"].(string)
	call(t, s, "GET", "/stickers/"+id, other, ``, 404)
	call(t, s, "DELETE", "/stickers/"+id, tok, ``, 200)
}

package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"nano_ctl/service/store"
)

func TestAliasHTTPWithoutHardware(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := New(nil, db).Handler()
	request := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		out := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(out, req)
		if out.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, out.Code, out.Body.String())
		}
		return out
	}
	request(http.MethodPost, "/api/v1/cards/0/open", "", http.StatusNotFound)
	request(http.MethodPost, "/api/v1/cards/0/close", "", http.StatusNotFound)
	path := "/api/v1/io-variables/0/output-bit/65535"
	request(http.MethodPut, path, `{"alias":"测试输出","reverse":true,"disabled":true}`, http.StatusOK)
	out := request(http.MethodGet, "/api/v1/io-variables?card_no=0", "", http.StatusOK)
	var body struct {
		Data []store.IOAlias `json:"data"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil || len(body.Data) != 1 || body.Data[0].Alias != "测试输出" {
		t.Fatalf("list: %s %v", out.Body.String(), err)
	}
	// These must fail before attempting any call on the nil hardware controller.
	request(http.MethodGet, "/api/v1/io-variables/0/output-bit/65535/value", "", http.StatusConflict)
	request(http.MethodPut, "/api/v1/io-variables/0/output-bit/65535/value", `{"on":false}`, http.StatusConflict)
	request(http.MethodPut, path, `{"alias":"  "}`, http.StatusBadRequest)
	request(http.MethodPut, "/api/v1/io-variables/0/output-bit/65536", `{"alias":"overflow"}`, http.StatusBadRequest)
	request(http.MethodGet, "/api/v1/io-variables?card_no=-1", "", http.StatusBadRequest)
	request(http.MethodPut, "/api/v1/io-variables/0/output-bit/1/value", `{}`, http.StatusBadRequest)
	request(http.MethodDelete, path, "", http.StatusNoContent)
	request(http.MethodGet, path, "", http.StatusNotFound)
}

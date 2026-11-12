package parser

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ragflow/internal/common"
)

func TestPDFParser_ParseWithResult_YoloWPDFJSONIntegration(t *testing.T) {
	withSSRFBypass(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/file_parse" {
			http.NotFound(w, r)
			return
		}
		if got, want := r.Header.Get("Authorization"), "Bearer yolow-secret"; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
			return
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Errorf("MultipartReader: %v", err)
			return
		}
		sawFile := false
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("NextPart: %v", err)
				return
			}
			if part.FormName() != "file" {
				t.Errorf("unexpected form field %q", part.FormName())
				return
			}
			body, _ := io.ReadAll(part)
			if !strings.HasPrefix(string(body), "%PDF") {
				t.Errorf("uploaded file = %q, want PDF bytes", string(body))
				return
			}
			sawFile = true
		}
		if !sawFile {
			t.Error("multipart file missing")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"json_doc":{"type":"title","content":"Yolo Title","children":[{"type":"paragraph","content":"Yolo Body"},{"type":"table","html":"<table><tr><td>a</td></tr></table>"}]}}`))
	}))
	defer server.Close()

	pdf := NewPDFParser()
	pdf.ConfigureFromSetup(map[string]any{
		"parse_method":       "YoloWPDF",
		"yolowpdf_apiserver": server.URL,
		"yolowpdf_api_key":   "yolow-secret",
	})
	res := pdf.ParseWithResult(t.Context(), "sample.pdf", []byte("%PDF-1.4\nmock"))
	if res.Err != nil {
		t.Fatalf("ParseWithResult: %v", res.Err)
	}
	if !res.Produced() {
		t.Fatal("produced = false, want true")
	}
	if len(res.JSON) < 2 {
		t.Fatalf("JSON len = %d, want >=2", len(res.JSON))
	}
}

func TestPDFParser_ParseWithResult_YoloWPDFMarkdownFallback(t *testing.T) {
	withSSRFBypass(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/file_parse" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"json_doc":null,"md_text":"# Yolo Title\n\nYolo Body\n"}`))
	}))
	defer server.Close()

	pdf := NewPDFParser()
	pdf.ConfigureFromSetup(map[string]any{
		"parse_method":       "yolowpdf",
		"yolowpdf_apiserver": server.URL,
	})
	res := pdf.ParseWithResult(t.Context(), "sample.pdf", []byte("%PDF-1.4\nmock"))
	if res.Err != nil {
		t.Fatalf("ParseWithResult: %v", res.Err)
	}
	var text strings.Builder
	for _, item := range ItemsToMaps(res.JSON) {
		value, _ := item["text"].(string)
		text.WriteString(value)
		text.WriteByte('\n')
	}
	if !strings.Contains(text.String(), "Yolo Title") {
		t.Fatalf("JSON text = %q, want Yolo Title", text.String())
	}
}

func TestPDFParser_ParseWithResult_YoloWPDFRequiresAPIServer(t *testing.T) {
	t.Setenv(common.EnvYoloWPDFAPIServer, "")
	pdf := NewPDFParser()
	pdf.ConfigureFromSetup(map[string]any{"parse_method": "YoloWPDF"})
	res := pdf.ParseWithResult(t.Context(), "sample.pdf", []byte("%PDF-1.4\nmock"))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "yolowpdf_apiserver") {
		t.Fatalf("error = %v, want yolowpdf_apiserver context", res.Err)
	}
}

func TestPDFParser_ParseWithResult_YoloWPDFUsesEnvironmentEndpoint(t *testing.T) {
	withSSRFBypass(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer env-yolow-key"; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		_, _ = w.Write([]byte(`{"md_text":"from env"}`))
	}))
	defer server.Close()
	t.Setenv(common.EnvYoloWPDFAPIServer, server.URL)
	t.Setenv(common.EnvYoloWPDFAPIKey, "env-yolow-key")

	pdf := NewPDFParser()
	pdf.ConfigureFromSetup(map[string]any{"parse_method": "YoloWPDF"})
	res := pdf.ParseWithResult(t.Context(), "sample.pdf", []byte("%PDF-1.4\nmock"))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	body := ""
	for _, item := range ItemsToMaps(res.JSON) {
		value, _ := item["text"].(string)
		body += value
	}
	if !strings.Contains(body, "from env") {
		t.Fatalf("text = %q, want from env", body)
	}
}

func TestPDFParser_ParseWithResult_YoloWPDFAppliesConfiguredTimeout(t *testing.T) {
	withSSRFBypass(t)
	server := openDataLoaderStallServer(t, 3*time.Second)
	defer server.Close()

	pdf := NewPDFParser()
	pdf.ConfigureFromSetup(map[string]any{
		"parse_method":       "YoloWPDF",
		"yolowpdf_apiserver": server.URL,
		"yolowpdf_timeout":   1,
	})
	start := time.Now()
	res := pdf.ParseWithResult(t.Context(), "sample.pdf", []byte("%PDF-1.4\nmock"))
	elapsed := time.Since(start)
	if !errors.Is(res.Err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", res.Err)
	}
	if elapsed < 900*time.Millisecond || elapsed >= 2500*time.Millisecond {
		t.Fatalf("elapsed = %s, want the request cut off at the configured 1s", elapsed)
	}
}

func TestPDFParser_ParseWithResult_YoloWPDFAbortsOnCancelledContext(t *testing.T) {
	withSSRFBypass(t)
	server := openDataLoaderStallServer(t, 3*time.Second)
	defer server.Close()

	pdf := NewPDFParser()
	pdf.ConfigureFromSetup(map[string]any{
		"parse_method":       "YoloWPDF",
		"yolowpdf_apiserver": server.URL,
	})
	pdf.YoloWPDFTimeout = 0
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	res := pdf.ParseWithResult(ctx, "sample.pdf", []byte("%PDF-1.4\nmock"))
	if !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", res.Err)
	}
}

func TestPDFParser_ParseWithResult_YoloWPDFSelectedInstanceIgnoresEnvironment(t *testing.T) {
	withSSRFBypass(t)
	t.Setenv(common.EnvYoloWPDFAPIServer, "http://environment.invalid")
	t.Setenv(common.EnvYoloWPDFAPIKey, "environment-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Authorization = %q, want empty", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"md_text":"instance"}`))
	}))
	defer server.Close()

	pdf := NewPDFParser()
	pdf.ParseMethod = "yolowpdf"
	pdf.YoloWPDFAPIServer = server.URL
	pdf.UseTenantModelConfig = true
	res := pdf.ParseWithResult(t.Context(), "sample.pdf", []byte("%PDF-1.4\nmock"))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
}

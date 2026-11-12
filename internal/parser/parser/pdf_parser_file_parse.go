package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"ragflow/internal/common"
)

// fileParseField is one extra multipart field sent with the PDF.
type fileParseField struct {
	name  string
	value string
}

// fileParseRequest is a remote parser that speaks the structured /file_parse
// contract: multipart POST of the PDF, JSON body with json_doc and md_text.
type fileParseRequest struct {
	backend         string
	endpointSetting string
	baseURL         string
	apiKey          string
	timeout         time.Duration
	fields          []fileParseField
}

// parsePDFWithStructuredFileParse posts the PDF and normalizes json_doc, or
// md_text when the service returns no structured elements. json_doc uses the
// OpenDataLoader element schema (typed nodes, PDF-point boxes, optional cells),
// which is the contract a compatible service such as yolowpdf publishes.
func parsePDFWithStructuredFileParse(ctx context.Context, filename string, data []byte, req fileParseRequest) ParseResult {
	if len(data) == 0 {
		return emptyPDFResult(filename)
	}
	baseURL := strings.TrimSpace(req.baseURL)
	if baseURL == "" {
		return ParseResult{Err: fmt.Errorf("parser: %s requires %s", req.backend, req.endpointSetting)}
	}
	bodyReader, contentType, err := structuredFileParseMultipart(req.backend, filename, data, req.fields)
	if err != nil {
		return ParseResult{Err: err}
	}
	timeout := req.timeout
	if timeout <= 0 {
		timeout = fileParseTimeoutSeconds * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/file_parse", bodyReader)
	if err != nil {
		return ParseResult{Err: fmt.Errorf("parser: %s request: %w", req.backend, err)}
	}
	httpReq.Header.Set("Content-Type", contentType)
	if apiKey := strings.TrimSpace(req.apiKey); apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := common.GetSSRFHTTPClient().Do(httpReq)
	if err != nil {
		return ParseResult{Err: fmt.Errorf("parser: %s submit: %w", req.backend, err)}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ParseResult{Err: fmt.Errorf("parser: %s read: %w", req.backend, err)}
	}
	if resp.StatusCode >= 300 {
		return ParseResult{Err: fmt.Errorf("parser: %s HTTP %d: %s", req.backend, resp.StatusCode, string(raw))}
	}
	var payload struct {
		JSONDoc any    `json:"json_doc"`
		MDText  string `json:"md_text"`
	}
	if err = json.Unmarshal(raw, &payload); err != nil {
		return ParseResult{Err: fmt.Errorf("parser: %s decode: %w", req.backend, err)}
	}
	if payload.JSONDoc != nil {
		sizes, pageCount, geometryErr := openDataLoaderPageGeometry(ctx, data, openDataLoaderPageIndexes(payload.JSONDoc))
		items, boxWarnings := fileParseItems(req.backend, payload.JSONDoc, sizes)
		if len(items) > 0 {
			if pageCount <= 0 {
				pageCount = openDataLoaderReportedPageCount(payload.JSONDoc)
			}
			res := pdfItemsToResult(filename, items, pageCount)
			if geometryErr != nil {
				res.Warnings = append(res.Warnings, req.backend+" PDF geometry unavailable: "+geometryErr.Error())
			}
			res.Warnings = append(res.Warnings, boxWarnings...)
			return res
		}
	}
	if strings.TrimSpace(payload.MDText) != "" {
		return pdfMarkdownToResult(ctx, filename, payload.MDText, 1)
	}
	return ParseResult{Err: fmt.Errorf("parser: %s returned no parsed content", req.backend)}
}

func structuredFileParseMultipart(backend, filename string, data []byte, fields []fileParseField) (io.Reader, string, error) {
	var body strings.Builder
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, "", fmt.Errorf("parser: %s create form file: %w", backend, err)
	}
	if _, err = part.Write(data); err != nil {
		return nil, "", fmt.Errorf("parser: %s write PDF: %w", backend, err)
	}
	for _, field := range fields {
		if err = writer.WriteField(field.name, field.value); err != nil {
			return nil, "", fmt.Errorf("parser: %s write field %s: %w", backend, field.name, err)
		}
	}
	if err = writer.Close(); err != nil {
		return nil, "", fmt.Errorf("parser: %s finalize form: %w", backend, err)
	}
	return strings.NewReader(body.String()), writer.FormDataContentType(), nil
}

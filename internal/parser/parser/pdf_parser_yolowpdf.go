package parser

import (
	"context"
	"strings"
	"time"

	"ragflow/internal/common"
)

// parsePDFWithYoloWPDF calls a yolowpdf service that speaks the structured
// /file_parse contract: multipart POST of the PDF, JSON with json_doc and
// md_text. Explicit tenant configuration never borrows a global endpoint or key.
func parsePDFWithYoloWPDF(ctx context.Context, filename string, data []byte, parser *PDFParser) ParseResult {
	baseURL := strings.TrimSpace(parser.YoloWPDFAPIServer)
	if baseURL == "" && !parser.UseTenantModelConfig {
		baseURL = strings.TrimSpace(common.GetEnv(common.EnvYoloWPDFAPIServer))
	}
	apiKey := strings.TrimSpace(parser.YoloWPDFAPIKey)
	if apiKey == "" && !parser.UseTenantModelConfig {
		apiKey = strings.TrimSpace(common.GetEnv(common.EnvYoloWPDFAPIKey))
	}
	return parsePDFWithStructuredFileParse(ctx, filename, data, fileParseRequest{
		backend:         "YoloWPDF",
		endpointSetting: "yolowpdf_apiserver or YOLOWPDF_APISERVER",
		baseURL:         baseURL,
		apiKey:          apiKey,
		timeout:         time.Duration(parser.YoloWPDFTimeout) * time.Second,
	})
}

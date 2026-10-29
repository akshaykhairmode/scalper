// Package main compresses static web assets with Brotli.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/andybalholm/brotli"
)

func main() {
	staticDir := filepath.Clean(filepath.Join("internal", "web", "static"))
	entries, err := os.ReadDir(staticDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read static dir: %v\n", err)
		os.Exit(1)
	}

	totalRaw := int64(0)
	totalBr := int64(0)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".br") {
			continue
		}
		ext := filepath.Ext(name)
		switch ext {
		case ".js", ".css", ".html", ".svg", ".json", ".txt":
		default:
			continue
		}

		srcPath := filepath.Clean(filepath.Join(staticDir, name))
		dstPath := srcPath + ".br"

		data, err := os.ReadFile(srcPath) // #nosec G304
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", srcPath, err)
			os.Exit(1)
		}

		var buf bytes.Buffer
		w := brotli.NewWriterOptions(&buf, brotli.WriterOptions{
			Quality: brotli.BestCompression,
		})
		if _, err := io.Copy(w, bytes.NewReader(data)); err != nil {
			fmt.Fprintf(os.Stderr, "compress %s: %v\n", srcPath, err)
			os.Exit(1)
		}
		if err := w.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "close brotli writer for %s: %v\n", srcPath, err)
			os.Exit(1)
		}

		brData := buf.Bytes()
		if err := os.WriteFile(dstPath, brData, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", dstPath, err)
			os.Exit(1)
		}

		rawSize := int64(len(data))
		brSize := int64(len(brData))
		totalRaw += rawSize
		totalBr += brSize

		savings := 100.0 * float64(rawSize-brSize) / float64(rawSize)
		fmt.Printf("compressed %-24s %7d B -> %6d B (%.1f%% saved)\n", name, rawSize, brSize, savings)
	}

	totalSavings := 100.0 * float64(totalRaw-totalBr) / float64(totalRaw)
	fmt.Printf("\nTotal: %d B -> %d B (%.1f%% saved)\n", totalRaw, totalBr, totalSavings)
}

package hsm

import (
	"log/slog"
	"strconv"
	"testing"
)

// BenchmarkHandleBinaryMessage measures the optimized zero-copy lock-free path.
func BenchmarkHandleBinaryMessage(b *testing.B) {
	quotes := make(chan *Quote, 1000)
	client := NewHSMClientWithLogger("ws://dummy", quotes, slog.New(slog.NewTextHandler(testDiscard{}, nil)))

	client.mu.Lock()
	client.dividers[2] = 100
	for i := 0; i < 2000; i++ {
		key := "nse_fo|" + strconv.Itoa(10000+i)
		client.tradingSymbols[key] = "NIFTY-STRIKE-" + strconv.Itoa(i)
	}
	client.updateMetadataSnapshotLocked()
	client.mu.Unlock()

	var batch []byte
	pkt := testMarketPicturePacket()
	for i := 0; i < 5; i++ {
		batch = append(batch, pkt...)
	}

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-quotes:
			}
		}
	}()
	defer close(done)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := client.handleBinaryMessage(batch); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHandleBinaryMessage_LegacyMapCopy measures the former map-copy-per-message behavior.
func BenchmarkHandleBinaryMessage_LegacyMapCopy(b *testing.B) {
	dividers := map[int8]int{2: 100}
	symbols := make(map[string]string, 2000)
	for i := 0; i < 2000; i++ {
		key := "nse_fo|" + strconv.Itoa(10000+i)
		symbols[key] = "NIFTY-STRIKE-" + strconv.Itoa(i)
	}

	var batch []byte
	pkt := testMarketPicturePacket()
	for i := 0; i < 5; i++ {
		batch = append(batch, pkt...)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		packets, err := splitSFeedBatch(batch)
		if err != nil {
			b.Fatal(err)
		}
		// Legacy behavior: copy maps on each message
		divCopy := make(map[int8]int, len(dividers))
		for id, div := range dividers {
			divCopy[id] = div
		}
		symCopy := make(map[string]string, len(symbols))
		for key, sym := range symbols {
			symCopy[key] = sym
		}
		for _, packet := range packets {
			quote, err := decodeSFeedPacket(packet, divCopy, symCopy)
			if err != nil {
				b.Fatal(err)
			}
			_ = quote
		}
	}
}

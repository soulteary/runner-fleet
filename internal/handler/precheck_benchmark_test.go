package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkInspectInstallDir(b *testing.B) {
	for _, files := range []int{0, 1, 1000, 10000} {
		b.Run(fmt.Sprintf("files=%d", files), func(b *testing.B) {
			dir := b.TempDir()
			for i := 0; i < files; i++ {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%06d", i)), nil, 0600); err != nil {
					b.Fatal(err)
				}
			}
			want := ""
			if files > 0 {
				want = "nonempty"
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := inspectInstallDir(dir); got != want {
					b.Fatalf("got %q want %q", got, want)
				}
			}
		})
	}
}

package pluginruntime

import (
	"testing"

	"github.com/hyaeve/manco/internal/source"
)

func TestKindForContentType(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{value: "MANGA", want: source.KindComic},
		{value: "HENTAI_MANGA", want: source.KindComic},
		{value: "MANHWA", want: source.KindComic},
		{value: "MANHUA", want: source.KindComic},
		{value: "NOVEL", want: source.KindBook},
		{value: "HENTAI_NOVEL", want: source.KindBook},
		{value: "VIDEO", want: source.KindOther},
		{value: "HENTAI_VIDEO", want: source.KindOther},
		{value: "", want: source.KindComic},
	}
	for _, test := range tests {
		if got := KindForContentType(test.value); got != test.want {
			t.Fatalf("KindForContentType(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}

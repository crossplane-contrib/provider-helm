package helm

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestEnsureContentCache(t *testing.T) {
	origCache := chartContentCache
	defer func() { chartContentCache = origCache }()

	type args struct {
		preexisting bool
		callers     int
	}
	type want struct {
		Failures int
		IsDir    bool
	}
	cases := map[string]struct {
		args args
		want want
	}{
		"CreatesMissingDirectory": {
			args: args{callers: 1},
			want: want{IsDir: true},
		},
		"ExistingDirectoryIsNotAnError": {
			args: args{preexisting: true, callers: 1},
			want: want{IsDir: true},
		},
		"ConcurrentCallersOnFreshPod": {
			args: args{callers: 100},
			want: want{IsDir: true},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			chartContentCache = filepath.Join(t.TempDir(), "content-cache")
			if tc.args.preexisting {
				if err := os.Mkdir(chartContentCache, 0750); err != nil {
					t.Fatalf("pre-creating %s: %v", chartContentCache, err)
				}
			}

			start := make(chan struct{})
			errs := make([]error, tc.args.callers)
			var wg sync.WaitGroup
			for i := range errs {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					errs[i] = EnsureContentCache()
				}(i)
			}
			close(start)
			wg.Wait()

			got := want{}
			for _, err := range errs {
				if err != nil {
					got.Failures++
				}
			}
			fi, err := os.Stat(chartContentCache)
			got.IsDir = err == nil && fi.IsDir()

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("EnsureContentCache(): -want, +got:\n%s", diff)
			}
		})
	}
}

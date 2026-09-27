package gcp

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

func TestCheckResources(t *testing.T) {
	ok := []config.Resources{{CPU: 1, Memory: "512Mi"}, {CPU: 4, Memory: "8Gi"}, {CPU: 8, Memory: "32Gi"}, {CPU: 4, Memory: "16Gi"}, {CPU: 2, Memory: "4Gi"}}
	for _, r := range ok {
		if issues := CheckResources(r); len(issues) > 0 {
			t.Errorf("%+v: %v", r, issues)
		}
	}
	bad := []struct {
		r            config.Resources
		field, words string
	}{
		{config.Resources{CPU: 3, Memory: "4Gi"}, "cpu", "one of 1, 2, 4, 6, 8"},
		{config.Resources{CPU: 16, Memory: "32Gi"}, "cpu", "one of 1, 2, 4, 6, 8"},
		{config.Resources{CPU: 8, Memory: "64Gi"}, "memory", "at most 32Gi"},
		{config.Resources{CPU: 1, Memory: "256Mi"}, "memory", "at least 512Mi"},
		{config.Resources{CPU: 1, Memory: "8Gi"}, "memory", "needs at least 2 CPUs"},
		{config.Resources{CPU: 2, Memory: "16Gi"}, "memory", "needs at least 4 CPUs"},
		{config.Resources{CPU: 4, Memory: "24Gi"}, "memory", "needs at least 6 CPUs"},
		{config.Resources{CPU: 6, Memory: "32Gi"}, "memory", "needs at least 8 CPUs"},
		{config.Resources{CPU: 4, Memory: "1Gi"}, "memory", "4 CPUs need at least 2Gi"},
		{config.Resources{CPU: 8, Memory: "2Gi"}, "memory", "8 CPUs need at least 4Gi"},
	}
	for _, tc := range bad {
		found := false
		for _, is := range CheckResources(tc.r) {
			found = found || (is.Field == tc.field && strings.Contains(is.Message, tc.words))
		}
		if !found {
			t.Errorf("%+v: want %s issue ~%q, got %v", tc.r, tc.field, tc.words, CheckResources(tc.r))
		}
	}
}

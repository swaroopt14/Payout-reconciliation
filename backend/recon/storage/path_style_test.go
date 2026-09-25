package storage

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestParseForcePathStyle(t *testing.T) {
	for in, want := range map[string]bool{
		"": false, "false": false, "0": false, "no": false, "yes": false,
		"true": true, "TRUE": true, " True ": true, "1": true,
	} {
		if got := parseForcePathStyle(in); got != want {
			t.Errorf("parseForcePathStyle(%q)=%v want %v", in, got, want)
		}
	}
}

func TestWithPathStyleFromEnv(t *testing.T) {
	t.Setenv(EnvS3ForcePathStyle, "")
	var o s3.Options
	withPathStyleFromEnv(&o)
	if o.UsePathStyle {
		t.Fatal("default must be virtual-hosted (off)")
	}
	t.Setenv(EnvS3ForcePathStyle, "1")
	withPathStyleFromEnv(&o)
	if !o.UsePathStyle {
		t.Fatal("S3_FORCE_PATH_STYLE=1 must enable path style")
	}
}

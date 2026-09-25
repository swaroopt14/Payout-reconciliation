package storage

import (
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// EnvS3ForcePathStyle switches the S3 client to path-style addressing
// (http://host/bucket/key), which MinIO/LocalStack need. Default off:
// real AWS keeps virtual-hosted addressing.
const EnvS3ForcePathStyle = "S3_FORCE_PATH_STYLE"

// parseForcePathStyle accepts "true" or "1" (case/space-insensitive); anything
// else, including empty, is false.
func parseForcePathStyle(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1":
		return true
	}
	return false
}

// ForcePathStyleFromEnv reads S3_FORCE_PATH_STYLE.
func ForcePathStyleFromEnv() bool { return parseForcePathStyle(os.Getenv(EnvS3ForcePathStyle)) }

// withPathStyleFromEnv is the s3.Options func used by NewS3Store.
func withPathStyleFromEnv(o *s3.Options) {
	if ForcePathStyleFromEnv() {
		o.UsePathStyle = true
	}
}

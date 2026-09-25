#!/bin/bash
# LocalStack ready.d hook: creates the harness S3 buckets and the KMS key
# with alias/clearline-e2e. Idempotent. Test-only; LocalStack accepts any
# credentials ("test"/"test").
set -euo pipefail
REGION="${AWS_DEFAULT_REGION:-ap-south-1}"
ALIAS="${E2E_KMS_ALIAS:-alias/clearline-e2e}"

for b in zord-edge-ingress zord-intent-engine-canonical zord-intent-engine-nir \
         zord-intent-engine-governance zord-outcome-engine-settlement-ingress; do
  if awslocal s3api head-bucket --bucket "$b" >/dev/null 2>&1; then
    echo "init-aws: bucket $b exists"
  else
    awslocal s3api create-bucket --bucket "$b" --region "$REGION" \
      --create-bucket-configuration "LocationConstraint=$REGION" >/dev/null
    echo "init-aws: created bucket $b"
  fi
done

if awslocal kms describe-key --key-id "$ALIAS" --region "$REGION" >/dev/null 2>&1; then
  echo "init-aws: $ALIAS exists"
else
  key_id="$(awslocal kms create-key --region "$REGION" --description clearline-e2e \
    --key-usage ENCRYPT_DECRYPT --query KeyMetadata.KeyId --output text)"
  awslocal kms create-alias --region "$REGION" --alias-name "$ALIAS" --target-key-id "$key_id"
  echo "init-aws: created KMS key and $ALIAS"
fi
touch /tmp/clearline-e2e-init-done

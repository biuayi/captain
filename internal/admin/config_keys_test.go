package admin_test

import (
	"slices"
	"testing"

	"github.com/hertz/captain/internal/admin"
)

// G3: super-admin must be able to set Cloudflare + Aliyun OSS/CDN/SMS tokens
// (原始需求：阿里云 access token = 对象存储/CDN/短信接口).
func TestConfigKeysCoverage(t *testing.T) {
	must := []string{
		"cloudflare_turnstile_sitekey", "cloudflare_turnstile_secret",
		"aliyun_oss_endpoint", "aliyun_oss_bucket",
		"aliyun_oss_key_id", "aliyun_oss_key_secret",
		"aliyun_cdn_domain",
		"aliyun_sms_access_key_id", "aliyun_sms_access_key_secret",
		"aliyun_sms_sign_name", "aliyun_sms_template_code",
	}
	for _, k := range must {
		if !slices.Contains(admin.ConfigKeys, k) {
			t.Errorf("ConfigKeys missing %q", k)
		}
	}
}

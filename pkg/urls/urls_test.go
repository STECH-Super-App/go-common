package urls

import "testing"

const (
	testBaseURL    = "https://app.stech.kz"
	testDeepScheme = "superapp"
)

func testEnv() Env {
	return Env{
		AppBaseURL:     testBaseURL,
		DeepLinkScheme: testDeepScheme,
	}
}

func TestOAuthCallbackVK(t *testing.T) {
	got := OAuthCallbackVK(testEnv())
	want := "https://app.stech.kz/auth/oauth2/vk/callback"
	if got != want {
		t.Errorf("OAuthCallbackVK() = %q; want %q", got, want)
	}
}

func TestOAuthCallbackYandex(t *testing.T) {
	got := OAuthCallbackYandex(testEnv())
	want := "https://app.stech.kz/auth/oauth2/yandex/callback"
	if got != want {
		t.Errorf("OAuthCallbackYandex() = %q; want %q", got, want)
	}
}

func TestMediaFile(t *testing.T) {
	got := MediaFile(testEnv(), "images/photo.jpg")
	want := "https://app.stech.kz/media/files/images/photo.jpg"
	if got != want {
		t.Errorf("MediaFile() = %q; want %q", got, want)
	}
}

func TestMediaFile_KeyWithSpaceAndSpecial(t *testing.T) {
	got := MediaFile(testEnv(), "user uploads/file?.png")
	want := "https://app.stech.kz/media/files/user%20uploads/file%3F.png"
	if got != want {
		t.Errorf("MediaFile() = %q; want %q", got, want)
	}
}

func TestPhoneVerification(t *testing.T) {
	got := PhoneVerification(testEnv(), "123456")
	want := "https://app.stech.kz/verify?code=123456&type=phone"
	if got != want {
		t.Errorf("PhoneVerification() = %q; want %q", got, want)
	}
}

func TestPhoneVerification_CodeNeedsEscape(t *testing.T) {
	got := PhoneVerification(testEnv(), "a&b=c")
	want := "https://app.stech.kz/verify?code=a%26b%3Dc&type=phone"
	if got != want {
		t.Errorf("PhoneVerification() = %q; want %q", got, want)
	}
}

func TestEmailVerification(t *testing.T) {
	got := EmailVerification(testEnv(), "abc123")
	want := "https://app.stech.kz/verify?code=abc123&type=email"
	if got != want {
		t.Errorf("EmailVerification() = %q; want %q", got, want)
	}
}

func TestUserProfile(t *testing.T) {
	got := UserProfile(testEnv(), "550e8400-e29b-41d4-a716-446655440000")
	want := "superapp://profile/550e8400-e29b-41d4-a716-446655440000"
	if got != want {
		t.Errorf("UserProfile() = %q; want %q", got, want)
	}
}

func TestTenantDashboard(t *testing.T) {
	got := TenantDashboard(testEnv(), "550e8400-e29b-41d4-a716-446655440001")
	want := "superapp://tenant/550e8400-e29b-41d4-a716-446655440001/dashboard"
	if got != want {
		t.Errorf("TenantDashboard() = %q; want %q", got, want)
	}
}

func TestTenantShare(t *testing.T) {
	got := TenantShare(testEnv(), "550e8400-e29b-41d4-a716-446655440002")
	want := "https://app.stech.kz/tenants/550e8400-e29b-41d4-a716-446655440002"
	if got != want {
		t.Errorf("TenantShare() = %q; want %q", got, want)
	}
}

func TestPasswordReset(t *testing.T) {
	got := PasswordReset(testEnv(), "mysecrettoken")
	want := "https://app.stech.kz/reset-password?token=mysecrettoken"
	if got != want {
		t.Errorf("PasswordReset() = %q; want %q", got, want)
	}
}

func TestPasswordReset_TokenNeedsEscape(t *testing.T) {
	got := PasswordReset(testEnv(), "a/b+c=d")
	want := "https://app.stech.kz/reset-password?token=a%2Fb%2Bc%3Dd"
	if got != want {
		t.Errorf("PasswordReset() = %q; want %q", got, want)
	}
}

package childenv

import "testing"

func TestWindowsEnvironmentNamesAreCaseInsensitive(t *testing.T) {
	got := Sanitize([]string{"basic_auth_password=secret", "Basic_Auth_User=admin", "agent_token=secret", "PATH=C:\\Tools"})
	if len(got) != 1 || got[0] != "PATH=C:\\Tools" {
		t.Fatalf("Windows credentials leaked: %v", got)
	}
}

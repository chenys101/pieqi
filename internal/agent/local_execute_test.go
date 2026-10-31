package agent

import "testing"

func TestLocalExecuteCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{`curl -s http://127.0.0.1:3000/ready`, true},
		{`curl -s http://localhost:3000/api/tasks`, true},
		{`curl -s http://example.com/`, false},
		{`curl -s https://127.0.0.1:3000/x`, true},
		{`wget -q -O - http://127.0.0.1:3000/`, true},
		{`Invoke-WebRequest http://127.0.0.1:3000/ready`, true},
		{`cd web; curl -s http://127.0.0.1:3000/ready`, true},
		{`curl -s http://127.0.0.1:3000/a; curl -s https://evil.example/`, false},
		{`go run ./cmd/pieqi`, false},
		{`curl -s http://127.0.0.1:3000/x | Out-File out.txt`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := LocalExecuteCommand(c.cmd); got != c.want {
			t.Errorf("%q → %v, want %v", c.cmd, got, c.want)
		}
	}
}

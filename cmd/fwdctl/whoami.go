package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// whoami says who the connection is: URL, login, organization and Forward version. It is the quickest proof that the credentials work.
func whoami(session func() (*fwd.Session, error), stdout, stderr io.Writer) int {
	s, err := session()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 3
	}
	ctx := context.Background()
	u, err := s.CurrentUser(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "error: the login does not work: %v\n", err)
		return 3
	}
	fmt.Fprintf(stdout, "url:          %s\n", os.Getenv("FORWARD_URL"))
	fmt.Fprintf(stdout, "login:        %s\n", u.Username)
	if o, err := s.Organization(ctx); err == nil {
		fmt.Fprintf(stdout, "organization: %s\n", o.Name)
	} else {
		fmt.Fprintf(stdout, "organization: (not readable: %v)\n", err)
	}
	if v, err := s.Version(ctx); err == nil {
		fmt.Fprintf(stdout, "forward:      %s\n", v.Release)
	}
	if s.Insecure() {
		fmt.Fprintln(stdout, "tls:          verification OFF (FORWARD_INSECURE)")
	}
	return 0
}

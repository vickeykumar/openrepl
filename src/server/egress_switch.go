package server

import "egress"

// The proxy for signed-in users' REPLs (LLD 16 "Egress") is on when the admin's switch in Site settings says so (on a worker: the gateway's
// switch, handed over with the site rules) or OPENREPL_EGRESS=public.
func init() { egress.SiteSwitch = func() bool { return GetSiteSettings().EgressOn } }

// egressSource says which of the two switches is on, for the Health page.
func egressSource() string {
	switch {
	case egress.Mode() == "public" && GetSiteSettings().EgressOn:
		return "Site settings and OPENREPL_EGRESS"
	case egress.Mode() == "public":
		return "OPENREPL_EGRESS"
	}
	return "Site settings"
}

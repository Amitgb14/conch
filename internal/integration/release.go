package integration

import "fmt"

// Releasing a deploy to production means different things on different
// services, and the person is asked first in that service's own terms:
// Vercel builds the commit again with production's environment, Netlify
// publishes the deploy as it was built and will not publish a deploy
// preview. The words and the rule are here, once, for every client.

// Release is how releasing is said for one deploy file: the button's
// verb, what happens, and the line under it.
type Release struct {
	Verb string `json:"verb"`
	What string `json:"what"`
	Note string `json:"note,omitempty"`
	// Rebuilds: releasing starts a new build for production (Vercel), so
	// production moves only once it is ready; else it is immediate.
	Rebuilds bool `json:"rebuilds,omitempty"`
}

// ReleaseOf is f's words for releasing a deploy.
func ReleaseOf(f *File) Release {
	switch f.Name {
	case "vercel":
		return Release{Verb: "Promote", Rebuilds: true,
			What: "This commit is built again for production, as Vercel's “Promote to Production” does. The production domains move to it once that build is ready.",
			Note: "It is built with production's environment variables: a preview's never reach production."}
	case "netlify":
		return Release{Verb: "Publish",
			What: "This deploy becomes the site's published deploy, as Netlify's “Publish deploy” does.",
			Note: "Nothing is built again: the deploy you checked is what goes live. Publish an earlier one the same way to roll back."}
	}
	return Release{Verb: "Promote",
		What: fmt.Sprintf("This deploy is released to production on %s: everybody using it sees it.", f.DisplayName()),
	}
}

// CanRelease says whether d may be released to production through f, and
// — for a ready deploy the service will not release — why not, in words
// for the row. A deploy that is not ready has nothing to say. A deploy
// already built for production may be released again: that is how an
// earlier one is rolled back to.
func CanRelease(f *File, d Deploy) (bool, string) {
	if f == nil || !f.Promote.Has() || d.State != "ready" {
		return false, ""
	}
	switch {
	case f.Name == "netlify" && d.Environment == "deploy-preview":
		return false, "A deploy preview can't be published. Publish a production-branch deploy — when auto publishing is locked a ready one waits for this — or an earlier one to roll back."
	case f.Name == "netlify" && d.Environment == "branch-deploy":
		return false, "A branch deploy can't be published. Publish a production-branch deploy — when auto publishing is locked a ready one waits for this — or an earlier one to roll back."
	}
	return true, ""
}

// Unlinked is what is said when f's service has nothing that builds from
// rem (Previews returned ErrUnlinked), with what conch cannot see yet.
func Unlinked(f *File, rem Remote) string {
	switch f.Name {
	case "vercel":
		return fmt.Sprintf("No Vercel project in your account is linked to %s/%s. Projects owned by a Vercel team aren't supported yet.", rem.Host, rem.Path)
	case "netlify":
		return fmt.Sprintf("No Netlify site in your account builds from %s. Only your first 100 sites are read.", rem.Path)
	}
	return fmt.Sprintf("Nothing on %s builds from %s/%s.", f.DisplayName(), rem.Host, rem.Path)
}

// HasBuildLog says whether f's failed_log reads a build's output. A
// failed_log that is one call reading the very field previews gives as
// the error (Netlify's error_message: its API has no build log) is the
// error message again, not a log.
func HasBuildLog(f *File) bool {
	if f == nil || !f.FailedLog.Has() {
		return false
	}
	op := f.FailedLog.Operation
	return !(len(op.Steps) == 0 && op.Text != "" && op.Text == f.Previews.Error)
}

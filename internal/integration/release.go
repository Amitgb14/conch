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
}

// ReleaseOf is f's words for releasing a deploy.
func ReleaseOf(f *File) Release {
	switch f.Name {
	case "vercel":
		return Release{Verb: "Promote",
			What: "This commit is built again for production, as Vercel's “Promote to Production” does. The production domains move to it once it is ready.",
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
		return false, "A deploy preview can't be published: merge, then publish the production branch's deploy"
	case f.Name == "netlify" && d.Environment == "branch-deploy":
		return false, "A branch deploy can't be published: merge, then publish the production branch's deploy"
	}
	return true, ""
}

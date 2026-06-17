package api42

// Endpoint is a personal, paginated sub-resource to pull for the authenticated
// user. Suffix is appended to "users/<id>/" to form the request path; File is
// the output filename (without extension).
type Endpoint struct {
	File   string
	Suffix string
}

// Collections lists every personal collection the fetcher pulls. Each becomes
// its own output/<File>.json. Per-endpoint failures are caught by the caller so
// one bad endpoint can't abort the whole run.
//
// Intentionally NOT listed here (learned from a real run):
//   - experiences, titles_users: role-gated (intra staff/tutor), not scope-gated;
//     a normal user token gets 403. titles_users is instead taken from the
//     embedded /me payload in the fetch package.
//   - exams_users: no working user-scoped variant exists. Exam results already
//     appear in projects_users ("Exam Rank 0X") and quests_users.
//   - patroned/patronages/expertises_users: empty for a typical account.
var Collections = []Endpoint{
	{File: "projects_users", Suffix: "projects_users"},
	{File: "cursus_users", Suffix: "cursus_users"},
	{File: "scale_teams", Suffix: "scale_teams"},
	{File: "scale_teams_as_corrector", Suffix: "scale_teams/as_corrector"},
	{File: "scale_teams_as_corrected", Suffix: "scale_teams/as_corrected"},
	{File: "locations", Suffix: "locations"},
	{File: "events_users", Suffix: "events_users"},
	{File: "correction_point_historics", Suffix: "correction_point_historics"},
	{File: "coalitions", Suffix: "coalitions"},
	{File: "quests_users", Suffix: "quests_users"},
}

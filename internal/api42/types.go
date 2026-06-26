package api42

import "encoding/json"

// Go types for the 42 v2 API responses, ported from the original hand-written
// types.ts (itself built from the actual JSON in output/*.json).
//
// Conventions:
//   - Date/time fields are ISO-8601 strings (e.g. "2026-06-02T11:25:56.991Z").
//   - A pointer (*string, *int, ...) is used where the API genuinely returns
//     null, so callers can tell "absent/null" from a real zero value.
//   - JSON keys ending in "?" (e.g. "validated?") are how the API names them;
//     they are ordinary keys, captured verbatim in the struct tag.
//   - Collections that always came back empty (partnerships, roles, ...) keep
//     their elements as json.RawMessage since their real shape is unknown.

// ----------------------------------------------------------------------------
// Shared building blocks
// ----------------------------------------------------------------------------

type ImageVersions struct {
	Large  *string `json:"large"`
	Medium *string `json:"medium"`
	Small  *string `json:"small"`
	Micro  *string `json:"micro"`
}

type Image struct {
	Link     *string       `json:"link"`
	Versions ImageVersions `json:"versions"`
}

type Group struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Language struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Identifier string `json:"identifier"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// UserShort is a minimal user reference, as embedded in scale_teams.
type UserShort struct {
	ID    int    `json:"id"`
	Login string `json:"login"`
	URL   string `json:"url"`
}

// User is the full user object embedded throughout the API (and the base of Me).
type User struct {
	ID              int     `json:"id"`
	Email           string  `json:"email"`
	Login           string  `json:"login"`
	FirstName       string  `json:"first_name"`
	LastName        string  `json:"last_name"`
	UsualFullName   string  `json:"usual_full_name"`
	UsualFirstName  *string `json:"usual_first_name"`
	URL             string  `json:"url"`
	Phone           *string `json:"phone"`
	Displayname     string  `json:"displayname"`
	Kind            string  `json:"kind"`
	Image           Image   `json:"image"`
	Staff           bool    `json:"staff?"`
	CorrectionPoint int     `json:"correction_point"`
	PoolMonth       *string `json:"pool_month"`
	PoolYear        *string `json:"pool_year"`
	Location        *string `json:"location"`
	Wallet          int     `json:"wallet"`
	AnonymizeDate   *string `json:"anonymize_date"`
	DataErasureDate *string `json:"data_erasure_date"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
	AlumnizedAt     *string `json:"alumnized_at"`
	Alumni          bool    `json:"alumni?"`
	Active          bool    `json:"active?"`
}

// ----------------------------------------------------------------------------
// /v2/me (also the per-user fields below)
// ----------------------------------------------------------------------------

type Skill struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Level float64 `json:"level"`
}

type Cursus struct {
	ID        int    `json:"id"`
	CreatedAt string `json:"created_at"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Kind      string `json:"kind"`
}

type CursusUser struct {
	ID           int     `json:"id"`
	BeginAt      string  `json:"begin_at"`
	EndAt        *string `json:"end_at"` // Wanted to use it, but it's unreliable because 42cursus alumni get null the same as active students
	Grade        *string `json:"grade"`
	Level        float64 `json:"level"`
	Skills       []Skill `json:"skills"`
	CursusID     int     `json:"cursus_id"`
	HasCoalition bool    `json:"has_coalition"`
	BlackholedAt *string `json:"blackholed_at"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	User         User    `json:"user"`
	Cursus       Cursus  `json:"cursus"`
}

type Achievement struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Tier         string `json:"tier"`
	Kind         string `json:"kind"`
	Visible      bool   `json:"visible"`
	Image        string `json:"image"`
	NbrOfSuccess *int   `json:"nbr_of_success"`
	UsersURL     string `json:"users_url"`
}

type Title struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type TitleUser struct {
	ID        int    `json:"id"`
	UserID    int    `json:"user_id"`
	TitleID   int    `json:"title_id"`
	Selected  bool   `json:"selected"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// CampusLanguage is the same shape as Language (TS aliased it).
type CampusLanguage = Language

type Campus struct {
	ID                 int            `json:"id"`
	Name               string         `json:"name"`
	TimeZone           string         `json:"time_zone"`
	Language           CampusLanguage `json:"language"`
	UsersCount         int            `json:"users_count"`
	VogsphereID        int            `json:"vogsphere_id"`
	Country            string         `json:"country"`
	Address            string         `json:"address"`
	Zip                string         `json:"zip"`
	City               string         `json:"city"`
	Website            string         `json:"website"`
	Facebook           string         `json:"facebook"`
	Twitter            string         `json:"twitter"`
	Active             bool           `json:"active"`
	Public             bool           `json:"public"`
	EmailExtension     string         `json:"email_extension"`
	DefaultHiddenPhone bool           `json:"default_hidden_phone"`
}

type CampusUser struct {
	ID        int    `json:"id"`
	UserID    int    `json:"user_id"`
	CampusID  int    `json:"campus_id"`
	IsPrimary bool   `json:"is_primary"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type LanguageUser struct {
	ID         int    `json:"id"`
	LanguageID int    `json:"language_id"`
	UserID     int    `json:"user_id"`
	Position   int    `json:"position"`
	CreatedAt  string `json:"created_at"`
}

// Me is the full /v2/me payload: a User plus the embedded collections. The
// embedded User promotes its JSON fields, mirroring TS's `Me extends User`.
type Me struct {
	User
	Groups          []Group           `json:"groups"`
	CursusUsers     []CursusUser      `json:"cursus_users"`
	ProjectsUsers   []ProjectUser     `json:"projects_users"`
	LanguagesUsers  []LanguageUser    `json:"languages_users"`
	Achievements    []Achievement     `json:"achievements"`
	Titles          []Title           `json:"titles"`
	TitlesUsers     []TitleUser       `json:"titles_users"`
	Partnerships    []json.RawMessage `json:"partnerships"`     // empty; shape unknown
	Patroned        []json.RawMessage `json:"patroned"`         // empty; shape unknown
	Patroning       []json.RawMessage `json:"patroning"`        // empty; shape unknown
	ExpertisesUsers []json.RawMessage `json:"expertises_users"` // empty; shape unknown
	Roles           []json.RawMessage `json:"roles"`            // empty; shape unknown
	Campus          []Campus          `json:"campus"`
	CampusUsers     []CampusUser      `json:"campus_users"`
}

// ----------------------------------------------------------------------------
// projects_users
// ----------------------------------------------------------------------------

type ProjectRef struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	ParentID *int   `json:"parent_id"`
}

type TeamUser struct {
	ID             int    `json:"id"`
	Login          string `json:"login"`
	URL            string `json:"url"`
	Leader         bool   `json:"leader"`
	Occurrence     int    `json:"occurrence"`
	Validated      bool   `json:"validated"`
	ProjectsUserID int    `json:"projects_user_id"`
}

type Team struct {
	ID                int        `json:"id"`
	Name              string     `json:"name"`
	URL               string     `json:"url"`
	FinalMark         *int       `json:"final_mark"`
	ProjectID         int        `json:"project_id"`
	CreatedAt         string     `json:"created_at"`
	UpdatedAt         string     `json:"updated_at"`
	Status            string     `json:"status"`
	TerminatingAt     *string    `json:"terminating_at"`
	Users             []TeamUser `json:"users"`
	Locked            bool       `json:"locked?"`
	Validated         bool       `json:"validated?"`
	Closed            bool       `json:"closed?"`
	RepoURL           *string    `json:"repo_url"`
	RepoUUID          string     `json:"repo_uuid"`
	LockedAt          *string    `json:"locked_at"`
	ClosedAt          *string    `json:"closed_at"`
	ProjectSessionID  int        `json:"project_session_id"`
	ProjectGitlabPath *string    `json:"project_gitlab_path"`
}

type ProjectUser struct {
	ID            int        `json:"id"`
	Occurrence    int        `json:"occurrence"`
	FinalMark     *int       `json:"final_mark"`
	Status        string     `json:"status"` // "finished" | "in_progress" | ...
	Validated     *bool      `json:"validated?"`
	CurrentTeamID *int       `json:"current_team_id"`
	Project       ProjectRef `json:"project"`
	CursusIds     []int      `json:"cursus_ids"`
	MarkedAt      *string    `json:"marked_at"`
	Marked        bool       `json:"marked"`
	RetriableAt   *string    `json:"retriable_at"`
	CreatedAt     string     `json:"created_at"`
	UpdatedAt     string     `json:"updated_at"`
	User          User       `json:"user"`
	Teams         []Team     `json:"teams"`
}

// ----------------------------------------------------------------------------
// scale_teams (evaluations)
// ----------------------------------------------------------------------------

type Flag struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Positive  bool   `json:"positive"`
	Icon      string `json:"icon"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type Scale struct {
	ID                 int        `json:"id"`
	EvaluationID       int        `json:"evaluation_id"`
	Name               string     `json:"name"`
	IsPrimary          bool       `json:"is_primary"`
	Comment            string     `json:"comment"`
	IntroductionMd     string     `json:"introduction_md"`
	DisclaimerMd       string     `json:"disclaimer_md"`
	GuidelinesMd       string     `json:"guidelines_md"`
	CreatedAt          string     `json:"created_at"`
	CorrectionNumber   int        `json:"correction_number"`
	Duration           int        `json:"duration"`
	ManualSubscription bool       `json:"manual_subscription"`
	Languages          []Language `json:"languages"`
	Flags              []Flag     `json:"flags"`
	Free               bool       `json:"free"`
}

// Feedback is a peer-evaluation rating left on a scale_team.
type Feedback struct {
	ID               int       `json:"id"`
	User             UserShort `json:"user"`
	FeedbackableType string    `json:"feedbackable_type"`
	FeedbackableID   int       `json:"feedbackable_id"`
	Comment          string    `json:"comment"`
	Rating           int       `json:"rating"`
	CreatedAt        string    `json:"created_at"`
}

type ScaleTeam struct {
	ID        int     `json:"id"`
	ScaleID   int     `json:"scale_id"`
	Comment   *string `json:"comment"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	Feedback  *string `json:"feedback"`
	FinalMark *int    `json:"final_mark"`
	Flag      Flag    `json:"flag"`
	BeginAt   string  `json:"begin_at"`
	// Truant is the zero value (ID 0) when nobody was truant — the API returns
	// an empty object {} in that case rather than a user reference.
	Correcteds           []UserShort       `json:"correcteds"`
	Corrector            UserShort         `json:"corrector"`
	Truant               UserShort         `json:"truant"`
	FilledAt             *string           `json:"filled_at"`
	QuestionsWithAnswers []json.RawMessage `json:"questions_with_answers"` // empty; shape unknown
	Feedbacks            []Feedback        `json:"feedbacks"`
	Scale                Scale             `json:"scale"`
	Team                 Team              `json:"team"`
}

// ----------------------------------------------------------------------------
// locations
// ----------------------------------------------------------------------------

type Location struct {
	ID       int     `json:"id"`
	BeginAt  string  `json:"begin_at"`
	EndAt    *string `json:"end_at"`
	Primary  bool    `json:"primary"`
	Host     string  `json:"host"`
	CampusID int     `json:"campus_id"`
	User     User    `json:"user"`
}

// ----------------------------------------------------------------------------
// events_users
// ----------------------------------------------------------------------------

type Event struct {
	ID                        int             `json:"id"`
	Name                      string          `json:"name"`
	Description               string          `json:"description"`
	Location                  string          `json:"location"`
	Kind                      string          `json:"kind"`
	MaxPeople                 *int            `json:"max_people"`
	NbrSubscribers            int             `json:"nbr_subscribers"`
	BeginAt                   string          `json:"begin_at"`
	EndAt                     string          `json:"end_at"`
	CampusIds                 []int           `json:"campus_ids"`
	CursusIds                 []int           `json:"cursus_ids"`
	CreatedAt                 string          `json:"created_at"`
	UpdatedAt                 string          `json:"updated_at"`
	ProhibitionOfCancellation *int            `json:"prohibition_of_cancellation"`
	Waitlist                  json.RawMessage `json:"waitlist"` // shape unknown / null
}

type EventUser struct {
	ID      int   `json:"id"`
	EventID int   `json:"event_id"`
	UserID  int   `json:"user_id"`
	User    User  `json:"user"`
	Event   Event `json:"event"`
}

// ----------------------------------------------------------------------------
// correction_point_historics
// ----------------------------------------------------------------------------

type CorrectionPointHistoric struct {
	ID          int    `json:"id"`
	ScaleTeamID *int   `json:"scale_team_id"`
	Reason      string `json:"reason"`
	Sum         int    `json:"sum"`
	Total       int    `json:"total"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// ----------------------------------------------------------------------------
// coalitions
// ----------------------------------------------------------------------------

type Coalition struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	ImageURL string `json:"image_url"`
	CoverURL string `json:"cover_url"`
	Color    string `json:"color"`
	Score    int    `json:"score"`
	UserID   int    `json:"user_id"`
}

// ----------------------------------------------------------------------------
// quests_users
// ----------------------------------------------------------------------------

type Quest struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Kind         string `json:"kind"`
	InternalName string `json:"internal_name"`
	Description  string `json:"description"`
	CursusID     int    `json:"cursus_id"`
	CampusID     *int   `json:"campus_id"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	GradeID      *int   `json:"grade_id"`
	Position     int    `json:"position"`
}

type QuestUser struct {
	ID          int             `json:"id"`
	EndAt       *string         `json:"end_at"`
	QuestID     int             `json:"quest_id"`
	ValidatedAt *string         `json:"validated_at"`
	Prct        *float64        `json:"prct"`
	Advancement json.RawMessage `json:"advancement"` // shape unknown / null
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	User        User            `json:"user"`
	Quest       Quest           `json:"quest"`
}

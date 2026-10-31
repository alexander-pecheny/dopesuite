package hostpages

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dope/dope/domain/multifest"
	"dope/dope/storage/festaccess"
	"dope/dope/storage/store"
	"dope/dope/web/pages"
	"dope/dope/web/route"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"

	"code.pecheny.me/pecheny/dopesuite/dopecore/authcred"
	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
	"code.pecheny.me/pecheny/dopesuite/dopecore/invitelink"
)

// Host invite links (ADR-0030): an admin mints a link for one Game, or for one
// venue of a synch or multi fest (ADR-0032), and whoever follows it becomes a
// host limited to it. A jury link names the fest and makes its jury. The link's machine is dopecore/invitelink; this file is
// the dashboard section, the /join page and the writes both they and the API
// make.

// The expiry choices the dashboard offers, in hours; 0 is no expiry.
const (
	inviteTTLDay   = 24
	inviteTTLWeek  = 7 * inviteTTLDay
	inviteTTLMonth = 30 * inviteTTLDay
)

// festInvite is one link with the Game or venue it opens.
type festInvite struct {
	invitelink.View
	GameID           int64  `json:"game_id,omitempty"`
	MultiFestVenueID int64  `json:"multifest_venue_id,omitempty"`
	Jury             bool   `json:"jury,omitempty"`
	Opens            string `json:"opens"`
	URL              string `json:"url"`
}

// inviteKind is one of the three sorts of link: what it opens, how a link's
// scope finds its fest, and the paths that revoke and delete one, since the
// two tables number their links apart.
type inviteKind struct {
	links invitelink.Links
	fest  func(ctx context.Context, q invitelink.Querier, scopeID int64) (int64, error)
	path  string
}

var (
	gameInvites  = inviteKind{festaccess.GameInvites(), festaccess.GameFestID, "invites"}
	venueInvites = inviteKind{festaccess.VenueInvites(), festaccess.VenueFestID, "venue-invites"}
	juryInvites  = inviteKind{festaccess.JuryInvites(), festaccess.JuryFestID, "jury-invites"}
)

// inviteKinds are every sort, in the order a code is looked up.
var inviteKinds = []inviteKind{gameInvites, venueInvites, juryInvites}

// inviteError words one of invitelink's errors for a person; any other error
// passes through as itself.
func inviteError(err error) error {
	if a, ok := festaccess.InviteTexts().Answer(err); ok {
		return &route.Status{Code: a.Status, Msg: a.Msg}
	}
	return err
}

// loadFestInvites lists every link of the fest's venues, then of its Games,
// then its jury links, each one's newest first.
func (s *Server) loadFestInvites(ctx context.Context, festID int64, origin string) ([]festInvite, error) {
	db := s.h.Engine().DB
	festGames, err := LoadFestGames(ctx, db, festID)
	if err != nil {
		return nil, err
	}
	venues, err := multifest.List(ctx, db, festID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := []festInvite{}
	add := func(kind inviteKind, scopeID int64, inv festInvite) error {
		links, err := kind.links.List(ctx, db, scopeID)
		for _, v := range invitelink.Views(links, now) {
			inv.View, inv.URL = v, origin+"/join/"+v.Code
			out = append(out, inv)
		}
		return err
	}
	for _, v := range venues {
		if err := add(venueInvites, v.ID, festInvite{MultiFestVenueID: v.ID, Opens: v.Label()}); err != nil {
			return nil, err
		}
	}
	for _, g := range festGames {
		if err := add(gameInvites, g.ID, festInvite{GameID: g.ID, Opens: g.Title}); err != nil {
			return nil, err
		}
	}
	if err := add(juryInvites, festID, festInvite{Jury: true, Opens: dopestrings.Default.Host.Invites.JuryOption()}); err != nil {
		return nil, err
	}
	return out, nil
}

// MintGameInvite makes a link to one Game of the fest.
func (s *Server) MintGameInvite(ctx context.Context, festID, gameID, userID int64, req invitelink.MintRequest) error {
	if kind, err := multifest.FestKind(ctx, s.h.Engine().DB, festID); err != nil {
		return err
	} else if kind == multifest.Synch {
		return route.BadRequest(dopestrings.Default.Festaccess.Invite.SynchGame())
	}
	return s.mintInvite(ctx, gameInvites, festID, gameID, userID, req)
}

// MintVenueInvite makes a link to one venue of the fest.
func (s *Server) MintVenueInvite(ctx context.Context, festID, venueID, userID int64, req invitelink.MintRequest) error {
	return s.mintInvite(ctx, venueInvites, festID, venueID, userID, req)
}

// MintJuryInvite makes a link to the fest's jury.
func (s *Server) MintJuryInvite(ctx context.Context, festID, userID int64, req invitelink.MintRequest) error {
	return s.mintInvite(ctx, juryInvites, festID, festID, userID, req)
}

// mintInvite makes a link to a Game or venue of the fest. Nobody approves a
// join, so a request for approval is ignored.
func (s *Server) mintInvite(ctx context.Context, kind inviteKind, festID, scopeID, userID int64, req invitelink.MintRequest) error {
	req.RequiresApproval = false
	opt, err := req.Options()
	if err != nil {
		return inviteError(err)
	}
	code, err := authcred.NewInviteCode()
	if err != nil {
		return err
	}
	return s.saveAccess(ctx, festID, func(ctx context.Context, tx *sql.Tx) error {
		if owner, err := kind.fest(ctx, tx, scopeID); err != nil || owner != festID {
			return route.BadRequest(dopestrings.Default.Festaccess.Invite.OtherFest())
		}
		_, err := kind.links.Mint(ctx, tx, scopeID, userID, code, opt, time.Now())
		return err
	})
}

// ensureVenueInvites gives every venue that never had a link one, made in the
// name of the fest's creator, so the organizer only sends them out.
func (s *Server) ensureVenueInvites(ctx context.Context, festID int64) error {
	type bare struct{ venue, creator int64 }
	missing, err := store.CollectRows(ctx, s.h.Engine().DB, `
select v.id, f.created_by from multifest_venues v join fests f on f.id = v.fest_id
where v.fest_id = ? and not exists (select 1 from venue_invites i where i.multifest_venue_id = v.id)
order by v.position, v.id`, []any{festID}, func(rows *sql.Rows) (bare, error) {
		var b bare
		return b, rows.Scan(&b.venue, &b.creator)
	})
	if err != nil || len(missing) == 0 {
		return err
	}
	return s.saveAccess(ctx, festID, func(ctx context.Context, tx *sql.Tx) error {
		for _, m := range missing {
			code, err := authcred.NewInviteCode()
			if err != nil {
				return err
			}
			if _, err := venueInvites.links.Mint(ctx, tx, m.venue, m.creator, code, invitelink.Options{}, time.Now()); err != nil {
				return err
			}
		}
		return nil
	})
}

// changeInvite revokes or deletes one of the fest's links.
func (s *Server) changeInvite(ctx context.Context, kind inviteKind, festID, id int64, remove bool) error {
	links := kind.links
	return s.saveAccess(ctx, festID, func(ctx context.Context, tx *sql.Tx) error {
		lk, err := links.ByID(ctx, tx, id)
		if err != nil {
			return inviteError(err)
		}
		if owner, err := kind.fest(ctx, tx, lk.ScopeID); err != nil || owner != festID {
			return route.BadRequest(dopestrings.Default.Festaccess.Invite.OtherFest())
		}
		if remove {
			return links.Delete(ctx, tx, id)
		}
		return links.Revoke(ctx, tx, id, time.Now())
	})
}

// joined is what following a link opened: a Game, a venue, or the jury.
type joined struct {
	FestID           int64 `json:"fest_id"`
	GameID           int64 `json:"game_id,omitempty"`
	MultiFestVenueID int64 `json:"multifest_venue_id,omitempty"`
	Jury             bool  `json:"jury,omitempty"`
}

// inviteByCode finds the link a code names among every sort.
func (s *Server) inviteByCode(ctx context.Context, code string) (inviteKind, invitelink.Link, error) {
	for _, kind := range inviteKinds {
		lk, err := kind.links.ByCode(ctx, s.h.Engine().DB, code)
		if !errors.Is(err, invitelink.ErrNotFound) {
			return kind, lk, err
		}
	}
	return juryInvites, invitelink.Link{}, invitelink.ErrNotFound
}

// JoinInvite follows a link for this user, as one write to the link's fest,
// and answers what it opens.
func (s *Server) JoinInvite(ctx context.Context, code string, userID int64) (joined, error) {
	kind, lk, err := s.inviteByCode(ctx, code)
	if err != nil {
		return joined{}, inviteError(err)
	}
	out := joined{GameID: lk.ScopeID}
	switch kind.path {
	case venueInvites.path:
		out = joined{MultiFestVenueID: lk.ScopeID}
	case juryInvites.path:
		out = joined{Jury: true}
	}
	if out.FestID, err = kind.fest(ctx, s.h.Engine().DB, lk.ScopeID); err != nil {
		return joined{}, inviteError(err)
	}
	err = s.saveAccess(ctx, out.FestID, func(ctx context.Context, tx *sql.Tx) error {
		_, err := kind.links.Join(ctx, tx, code, userID, time.Now())
		return err
	})
	return out, inviteError(err)
}

// openURL is the page a followed link leads to: its Game, its venue's page,
// or for the jury the fest's.
func (s *Server) openURL(ctx context.Context, j joined) string {
	if j.GameID > 0 {
		return s.hostGameURL(ctx, j.FestID, j.GameID)
	}
	if j.Jury {
		return "/host/fest/" + s.festRefOrID(ctx, j.FestID)
	}
	return venuePageURL(s.festRefOrID(ctx, j.FestID), j.MultiFestVenueID)
}

// ---- the dashboard ----

// The dashboard's select names what a link opens as g<id> or v<id>, or j
// for the fest's jury.
const (
	opensGame  = "g"
	opensVenue = "v"
	opensJury  = "j"
)

func (s *Server) handleHostCreateInvite(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	opens := r.FormValue("opens")
	kind := gameInvites
	scopeID, _ := idstr.Parse(opens[min(1, len(opens)):])
	switch {
	case strings.HasPrefix(opens, opensVenue):
		kind = venueInvites
	case opens == opensJury:
		kind, scopeID = juryInvites, sc.FestID
	}
	maxUses, _ := idstr.Parse(strings.TrimSpace(r.FormValue("max_uses")))
	ttl, _ := idstr.Parse(r.FormValue("ttl_hours"))
	req := invitelink.MintRequest{Label: r.FormValue("label"), MaxUses: maxUses, TTLHours: ttl}
	if err := s.mintInvite(r.Context(), kind, sc.FestID, scopeID, sc.User.UserID, req); err != nil {
		return s.renderInviteMessage(w, r, sc.FestID, err)
	}
	s.renderHostFestDashboard(w, r, sc.FestID, hostDashMessages{InviteNotice: dopestrings.Default.Host.Invites.CreatedNotice()})
	return nil
}

func (s *Server) handleHostChangeInvite(kind inviteKind, remove bool) route.Handler {
	return func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		id, _ := idstr.Parse(r.PathValue("id"))
		if err := s.changeInvite(r.Context(), kind, sc.FestID, id, remove); err != nil {
			return s.renderInviteMessage(w, r, sc.FestID, err)
		}
		http.Redirect(w, r, "/host/fest/"+r.PathValue("fest")+"#invites", http.StatusSeeOther)
		return nil
	}
}

// renderInviteMessage shows a refusal a person can read on the dashboard and
// hands any other error back to the dispatcher.
func (s *Server) renderInviteMessage(w http.ResponseWriter, r *http.Request, festID int64, err error) error {
	var status *route.Status
	if !errors.As(err, &status) {
		return err
	}
	s.renderHostFestDashboard(w, r, festID, hostDashMessages{InviteError: status.Msg})
	return nil
}

func hostDashInvitesSection(data hostFestDashData, ref string) *dopeui.Element {
	s := dopestrings.Default
	synch := data.Kind == multifest.Synch
	lead := s.Host.Invites.Lead()
	switch {
	case synch:
		lead = s.Host.Invites.LeadSynch()
	case len(data.MultiFestVenues) > 0:
		lead = s.Host.Invites.LeadVenues()
	}
	sect := []dopeui.Item{dopeui.ID("invites"), dopeui.Subhead(dopeui.Text(s.Host.Invites.Subhead())), dopeui.Hint(dopeui.Text(lead))}
	if text := venueInviteLines(data.Invites); text != "" {
		sect = append(sect, dopeui.Row(dopeui.Button(dopeui.Data("copy", text), dopeui.Data("copied", s.Host.Invites.CopiedNotice()),
			dopeui.Text(s.Host.Invites.CopyVenues()))))
	}
	if data.InviteError != "" {
		sect = append(sect, dopeui.Empty(dopeui.Text(data.InviteError)))
	}
	if data.InviteNotice != "" {
		sect = append(sect, dopeui.Note(dopeui.Text(data.InviteNotice)))
	}
	if len(data.Invites) == 0 {
		sect = append(sect, dopeui.Empty(dopeui.Text(s.Host.Invites.Empty())))
	} else {
		rows := make([]dopeui.Item, 0, len(data.Invites))
		for _, inv := range data.Invites {
			rows = append(rows, inviteRow(inv, ref))
		}
		sect = append(sect, dopeui.Col(append([]dopeui.Item{dopeui.SpaceSM}, rows...)...))
	}
	opens := []dopeui.Item{dopeui.Name("opens")}
	opensLabel := s.Host.Invites.GameLabel()
	switch {
	case synch:
		opensLabel = s.Host.Invites.VenueLabel()
	case len(data.MultiFestVenues) > 0:
		opensLabel = s.Host.Invites.VenueOrGameLabel()
	}
	for _, v := range data.MultiFestVenues {
		opens = append(opens, dopeui.Option(dopeui.Value(opensVenue+idstr.Format(v.ID)), dopeui.Text(v.Label())))
	}
	for _, g := range data.Games {
		if g.MultiFestVenueID > 0 || synch {
			// A venue's link opens its Games too, and a synch fest invites to venues alone.
			continue
		}
		opens = append(opens, dopeui.Option(dopeui.Value(opensGame+idstr.Format(g.ID)), dopeui.Text(g.Title)))
	}
	if data.Contested {
		opens = append(opens, dopeui.Option(dopeui.Value(opensJury), dopeui.Text(s.Host.Invites.JuryOption())))
		sect = append(sect, dopeui.Hint(dopeui.Text(s.Host.Invites.JuryHint())))
	}
	ttl := func(hours int, label string) *dopeui.Element {
		return dopeui.Option(dopeui.Value(strconv.Itoa(hours)), dopeui.Text(label))
	}
	sect = append(sect, dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/invites#invites"), dopeui.Autocomplete("off"),
		dopeui.Field(dopeui.Label(opensLabel), dopeui.Selectfield(opens...)),
		dopeui.Field(dopeui.Label(s.Host.Invites.LabelLabel()),
			dopeui.Textfield(dopeui.Name("label"), dopeui.Maxlength(strconv.Itoa(invitelink.MaxLabelRunes)), dopeui.Placeholder(s.Host.Invites.LabelPlaceholder()))),
		dopeui.Field(dopeui.Label(s.Host.Invites.UsesLabel()), dopeui.Textfield(dopeui.Name("max_uses"), dopeui.Inputmode("numeric"))),
		dopeui.Field(dopeui.Label(s.Host.Invites.TtlLabel()), dopeui.Selectfield(dopeui.Name("ttl_hours"),
			ttl(0, s.Host.Invites.TtlNever()), ttl(inviteTTLDay, s.Host.Invites.TtlDay()), ttl(inviteTTLWeek, s.Host.Invites.TtlWeek()), ttl(inviteTTLMonth, s.Host.Invites.TtlMonth()))),
		dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Invites.CreateSubmit()))),
	))
	return dopeui.Section(sect...)
}

// venueInviteLines is one line per venue with a working link, its name and
// its newest such link, for the organizer to send out at once.
func venueInviteLines(invites []festInvite) string {
	var lines []string
	seen := map[int64]bool{}
	for _, inv := range invites {
		if inv.MultiFestVenueID == 0 || inv.State != string(invitelink.Active) || seen[inv.MultiFestVenueID] {
			continue
		}
		seen[inv.MultiFestVenueID] = true
		lines = append(lines, inv.Opens+" "+inv.URL)
	}
	return strings.Join(lines, "\n")
}

// inviteRow is one link: its name and facts, the URL to copy while it works,
// who came in, and its revoke and delete buttons.
func inviteRow(inv festInvite, ref string) *dopeui.Element {
	s := dopestrings.Default
	title := inv.Label
	if title == "" {
		title = inv.Opens
	}
	usage := s.Host.Invites.Usage(idstr.Format(inv.Used))
	if inv.Left != nil {
		usage = s.Host.Invites.UsageLeft(idstr.Format(inv.Used), idstr.Format(*inv.Left))
	}
	meta := s.Host.Invites.RowMeta(inv.Opens, inviteStateText(inv.State), usage)
	if inv.Label == "" {
		meta = s.Host.Invites.RowMetaUnnamed(inviteStateText(inv.State), usage)
	}
	facts := []dopeui.Item{dopeui.DirCol, dopeui.SpaceSM, dopeui.Strong(dopeui.Text(title)), dopeui.Muted(dopeui.Text(meta))}
	active := inv.State == string(invitelink.Active)
	if active && inv.ExpiresAt != "" {
		facts = append(facts, dopeui.Muted(dopeui.Text(s.Host.Invites.Expires(tokenDate(inv.ExpiresAt)))))
	}
	if active {
		facts = append(facts, dopeui.Textfield(dopeui.Readonly(), dopeui.Value(inv.URL), dopeui.Data("select-all", "")))
	}
	if len(inv.Joined) > 0 {
		names := make([]string, len(inv.Joined))
		for i, p := range inv.Joined {
			names[i] = p.Name
		}
		facts = append(facts, dopeui.Hint(dopeui.Text(s.Host.Invites.Joined(strings.Join(names, ", ")))))
	}
	kind := gameInvites
	switch {
	case inv.MultiFestVenueID > 0:
		kind = venueInvites
	case inv.Jury:
		kind = juryInvites
	}
	base := "/host/fest/" + ref + "/" + kind.path + "/" + idstr.Format(inv.ID)
	var buttons []dopeui.Item
	if active {
		buttons = append(buttons, dopeui.Form(dopeui.Method("post"), dopeui.Action(base+"/revoke"), dopeui.Data("confirm", s.Host.Invites.RevokeConfirm()),
			dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Invites.Revoke()))))
	}
	buttons = append(buttons, dopeui.Form(dopeui.Method("post"), dopeui.Action(base+"/delete"), dopeui.Data("confirm", s.Host.Invites.DeleteConfirm()),
		dopeui.Button(dopeui.Danger, dopeui.Submit(), dopeui.Text(s.Host.Invites.Delete()))))
	return dopeui.Card(append(facts, dopeui.Row(append([]dopeui.Item{dopeui.SpaceSM, dopeui.Wrap()}, buttons...)...))...)
}

func inviteStateText(state string) string {
	s := dopestrings.Default
	switch invitelink.State(state) {
	case invitelink.Revoked:
		return s.Host.Invites.StateRevoked()
	case invitelink.Expired:
		return s.Host.Invites.StateExpired()
	case invitelink.Exhausted:
		return s.Host.Invites.StateExhausted()
	}
	return s.Host.Invites.StateActive()
}

// ---- the page a link opens ----

// HandleJoin serves /join/<code>: who is not signed in signs in first and comes
// back; then GET says what the link offers and POST follows it.
func (s *Server) HandleJoin(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimPrefix(r.URL.Path, "/join/")
	if code == "" || strings.Contains(code, "/") {
		http.NotFound(w, r)
		return
	}
	user, ok := s.h.Engine().LookupSession(r)
	if !ok {
		http.Redirect(w, r, "/login?next="+url.QueryEscape("/join/"+code), http.StatusSeeOther)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.renderJoin(w, r, code, user.UserID, "")
	case http.MethodPost:
		if !route.SameOriginUnsafe(w, r) {
			return
		}
		j, err := s.JoinInvite(r.Context(), code, user.UserID)
		var status *route.Status
		switch {
		case errors.As(err, &status):
			s.renderJoin(w, r, code, user.UserID, status.Msg)
		case err != nil:
			route.WriteError(w, r, err)
		default:
			http.Redirect(w, r, s.openURL(r.Context(), j), http.StatusSeeOther)
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) hostGameURL(ctx context.Context, festID, gameID int64) string {
	return "/host/fest/" + s.festRefOrID(ctx, festID) + "/game/" + s.gameRefOrID(ctx, gameID) + "/"
}

// renderJoin draws the join page for the link's state as this user sees it;
// refusal, when set, is why following it just failed.
func (s *Server) renderJoin(w http.ResponseWriter, r *http.Request, code string, userID int64, refusal string) {
	str := dopestrings.Default
	kind, _, err := s.inviteByCode(r.Context(), code)
	var peek invitelink.Peek
	if err == nil {
		peek, err = kind.links.PeekCode(r.Context(), s.h.Engine().DB, code, userID, time.Now())
	}
	venue, jury := kind.path == venueInvites.path, kind.path == juryInvites.path
	var status *route.Status
	if err := inviteError(err); errors.As(err, &status) {
		refusal = status.Msg
	} else if err != nil {
		route.WriteError(w, r, err)
		return
	}
	var body []dopeui.Item
	switch {
	case refusal != "":
		body = []dopeui.Item{dopeui.Subhead(dopeui.Text(str.Host.Join.RefusedHeading())), dopeui.Hint(dopeui.Text(refusal))}
	case peek.State == invitelink.Member:
		festID, err := kind.fest(r.Context(), s.h.Engine().DB, peek.ScopeID)
		if err != nil {
			route.WriteError(w, r, err)
			return
		}
		open, label := s.openURL(r.Context(), joined{FestID: festID, GameID: peek.ScopeID}), str.Host.Join.OpenGame()
		heading := str.Host.Join.MemberHeading(peek.ScopeName)
		switch {
		case venue:
			open, label = s.openURL(r.Context(), joined{FestID: festID}), str.Host.Join.OpenFest()
		case jury:
			open, label = s.openURL(r.Context(), joined{FestID: festID, Jury: true}), str.Host.Join.OpenFest()
			heading = str.Host.Join.MemberHeadingJury()
		}
		body = []dopeui.Item{dopeui.Subhead(dopeui.Text(heading)),
			dopeui.Row(dopeui.Button(dopeui.Href(open), dopeui.Text(label)))}
	case peek.State == invitelink.Active:
		heading, note, submit := str.Host.Join.ActiveHeading(peek.ScopeName), str.Host.Join.ActiveNote(), str.Host.Join.JoinSubmit()
		switch {
		case venue:
			note = str.Host.Join.ActiveNoteVenue()
		case jury:
			heading, note, submit = str.Host.Join.ActiveHeadingJury(peek.ScopeName), str.Host.Join.ActiveNoteJury(), str.Host.Join.JoinSubmitJury()
		}
		body = []dopeui.Item{dopeui.Subhead(dopeui.Text(heading)), dopeui.Hint(dopeui.Text(note)),
			dopeui.Form(dopeui.Method("post"), dopeui.Action("/join/"+code),
				dopeui.Button(dopeui.Submit(), dopeui.Text(submit)))}
	default:
		body = []dopeui.Item{dopeui.Subhead(dopeui.Text(str.Host.Join.RefusedHeading())),
			dopeui.Hint(dopeui.Text(festaccess.InviteTexts().Refusal(peek.State)))}
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(
		dopeui.Title(str.Host.Join.Title()), dopeui.PagePublic,
		dopeui.Publictopbar(pages.Trail([]dopeui.Item{pages.HomeCrumb()}, str.Host.Join.Title())),
		dopeui.Section(body...),
	)}})
}

// ---- the JSON twins ----

func (s *Server) apiInvites(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	invites, err := s.loadFestInvites(r.Context(), sc.FestID, pages.PublicOrigin(r))
	if err != nil {
		return err
	}
	return route.JSON(w, invites)
}

func (s *Server) apiCreateInvite(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req invitelink.MintRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.MintGameInvite(r.Context(), sc.FestID, sc.GameID, sc.User.UserID, req); err != nil {
		return err
	}
	return s.apiInvites(w, r, sc)
}

func (s *Server) apiCreateVenueInvite(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req invitelink.MintRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	venueID, _ := idstr.Parse(r.PathValue("venue"))
	if err := s.MintVenueInvite(r.Context(), sc.FestID, venueID, sc.User.UserID, req); err != nil {
		return err
	}
	return s.apiInvites(w, r, sc)
}

func (s *Server) apiCreateJuryInvite(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req invitelink.MintRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.MintJuryInvite(r.Context(), sc.FestID, sc.User.UserID, req); err != nil {
		return err
	}
	return s.apiInvites(w, r, sc)
}

func (s *Server) apiChangeInvite(kind inviteKind, remove bool) route.Handler {
	return func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		id, _ := idstr.Parse(r.PathValue("id"))
		if err := s.changeInvite(r.Context(), kind, sc.FestID, id, remove); err != nil {
			return err
		}
		return s.apiInvites(w, r, sc)
	}
}

// apiJoin follows a link as the caller: the twin of the join page's button.
func (s *Server) apiJoin(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	j, err := s.JoinInvite(r.Context(), r.PathValue("code"), sc.User.UserID)
	if err != nil {
		return err
	}
	return route.JSON(w, j)
}

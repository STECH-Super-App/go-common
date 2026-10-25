package notifyrender

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"

	"github.com/STECH-Super-App/go-common/pkg/i18n"
)

// This file is the live proof for TESTIROVANIE-87 over the 13 membership
// notifications — the four tenant invitations, block/unblock, removal (to the
// member and to the admin), leaving, the two role changes and the two admin
// transfers.
//
// It pins two things the rest of the package cannot:
//
//  1. **The user-facing noun is "team", never "tenant"** (vault П-31: «Рабочее
//     слово бэкенда "тенант" в интерфейсе не используется»). The internal
//     vocabulary stays `tenant` everywhere — type names, Reason codes, the
//     {{.tenant_name}} param, every comment — so a plain grep for the word
//     cannot tell copy from contract. The guard below strips every {{…}}
//     action first and only then looks at the prose.
//
//  2. **Each one states the CONSEQUENCE for its reader** — what they can now
//     do, or can no longer do (П-28: ending membership immediately removes
//     access to the team's garage and listings and deactivates operator
//     assignments; unblocking returns membership but NOT the assignments).
//     Both halves are asserted on a RENDERED message, not on the map value:
//     `TestMembershipCopyRendersTheConsequenceFromTheBaseline` renders with no
//     overlay mounted at all, so the English it checks is the compiled Go
//     baseline, and `…FromTheRussianOverlay` renders the i18n-catalog Russian
//     through the real overlay engine. A string that lives in a bundle but
//     never reaches a rendered message is the exact failure mode of the
//     baseline+overlay split, so neither half is proven by the other.

// membershipCopyCase is one membership notification: the params its directive
// carries, and the clause each locale must keep. The clauses are deliberately
// written out rather than derived from BaselineEN — a test that reads its
// expectation out of the value it is checking proves nothing.
type membershipCopyCase struct {
	nt     notificationv1.NotificationType
	params map[string]string
	// consequenceEN / consequenceRU are substrings of the rendered BODY that
	// carry the "what changes for you" half of the message.
	consequenceEN string
	consequenceRU string
	// ruBody is the i18n-catalog Russian for this type, mounted as the overlay.
	ruTitle string
	ruBody  string
}

func membershipCopyCases() []membershipCopyCase {
	tn := map[string]string{"tenant_name": "Crew A"}
	invite := map[string]string{"tenant_name": "Crew A", "inviter_name": "Ivan"}

	return []membershipCopyCase{
		{
			nt:            notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_INVITE_ORGANISATION_MANAGER,
			params:        invite,
			consequenceEN: "Accept it to manage the team's machinery and listings.",
			consequenceRU: "Примите приглашение, чтобы вести технику и объявления команды.",
			ruTitle:       "Приглашение в {{.tenant_name}}",
			ruBody:        "{{.inviter_name}} приглашает вас в {{.tenant_name}} в роли менеджера. Примите приглашение, чтобы вести технику и объявления команды.",
		},
		{
			nt:            notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_INVITE_ORGANISATION_OPERATOR,
			params:        invite,
			consequenceEN: "Accept it to see the machinery the team assigns to you.",
			consequenceRU: "Примите приглашение, чтобы видеть технику, которую вам назначат.",
			ruTitle:       "Приглашение в {{.tenant_name}}",
			ruBody:        "{{.inviter_name}} приглашает вас в {{.tenant_name}} в роли оператора. Примите приглашение, чтобы видеть технику, которую вам назначат.",
		},
		{
			nt:            notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_INVITE_PERSONAL_MANAGER,
			params:        invite,
			consequenceEN: "Accept it to manage the team's machinery and listings.",
			consequenceRU: "Примите приглашение, чтобы вести технику и объявления команды.",
			ruTitle:       "Приглашение в команду",
			ruBody:        "{{.inviter_name}} приглашает вас в {{.tenant_name}} в роли менеджера. Примите приглашение, чтобы вести технику и объявления команды.",
		},
		{
			nt:            notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_INVITE_PERSONAL_OPERATOR,
			params:        invite,
			consequenceEN: "Accept it to see the machinery the team assigns to you.",
			consequenceRU: "Примите приглашение, чтобы видеть технику, которую вам назначат.",
			ruTitle:       "Приглашение в команду",
			ruBody:        "{{.inviter_name}} приглашает вас в {{.tenant_name}} в роли оператора. Примите приглашение, чтобы видеть технику, которую вам назначат.",
		},
		{
			nt:            notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFERRED_NEW,
			params:        tn,
			consequenceEN: "Its machinery, listings, operator assignments and open requests are now yours to manage.",
			consequenceRU: "Её техника, объявления, назначения операторов и открытые заявки теперь в вашем управлении.",
			ruTitle:       "Теперь вы администратор команды",
			ruBody:        "Вы назначены администратором команды {{.tenant_name}}. Её техника, объявления, назначения операторов и открытые заявки теперь в вашем управлении.",
		},
		{
			nt:            notificationv1.NotificationType_NOTIFICATION_TYPE_ORG_ADMIN_TRANSFERRED_OLD,
			params:        tn,
			consequenceEN: "You stay in it as a manager: its machinery and listings stay open to you, its members are now managed by the new admin.",
			consequenceRU: "Вы остаётесь в ней менеджером: техника и объявления остаются вам доступны, участниками теперь управляет новый администратор.",
			ruTitle:       "Права администратора переданы",
			ruBody:        "Вы передали права администратора команды {{.tenant_name}}. Вы остаётесь в ней менеджером: техника и объявления остаются вам доступны, участниками теперь управляет новый администратор.",
		},
		{
			nt:            notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_BLOCKED,
			params:        tn,
			consequenceEN: "Its machinery and listings are closed to you until the restriction is lifted.",
			consequenceRU: "Её техника и объявления закрыты для вас, пока ограничение не снимут.",
			ruTitle:       "Доступ временно ограничен",
			ruBody:        "Ваш доступ к команде {{.tenant_name}} временно ограничен. Её техника и объявления закрыты для вас, пока ограничение не снимут.",
		},
		{
			// П-28: unblocking returns membership but not the assignments.
			nt:            notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_UNBLOCKED,
			params:        tn,
			consequenceEN: "but your earlier machinery assignments were not returned",
			consequenceRU: "но прежние назначения на технику не вернулись",
			ruTitle:       "Доступ восстановлен",
			ruBody:        "Ваш доступ к команде {{.tenant_name}} восстановлен. Её техника и объявления снова открыты, но прежние назначения на технику не вернулись.",
		},
		{
			nt:            notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_REMOVED,
			params:        tn,
			consequenceEN: "Its machinery, listings and requests are closed to you.",
			consequenceRU: "Доступ к её технике, объявлениям и заявкам закрыт.",
			ruTitle:       "Удалены из команды",
			ruBody:        "Вы удалены из команды {{.tenant_name}}. Доступ к её технике, объявлениям и заявкам закрыт.",
		},
		{
			nt:            notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_MEMBER_REMOVED_ADMIN,
			params:        map[string]string{"tenant_name": "Crew A", "removed_member_name": "Ivan"},
			consequenceEN: "check whether any machinery is now left without an operator",
			consequenceRU: "проверьте, не осталась ли техника без оператора",
			ruTitle:       "{{.removed_member_name}} исключён из команды",
			ruBody:        "{{.removed_member_name}} исключён из команды {{.tenant_name}}. Доступ этого участника к технике и объявлениям закрыт — проверьте, не осталась ли техника без оператора.",
		},
		{
			nt:            notificationv1.NotificationType_NOTIFICATION_TYPE_TENANT_MEMBER_LEFT,
			params:        map[string]string{"tenant_name": "Crew A", "member_name": "Ivan"},
			consequenceEN: "check whether any machinery is now left without an operator",
			consequenceRU: "проверьте, не осталась ли техника без оператора",
			ruTitle:       "{{.member_name}} покинул команду",
			ruBody:        "{{.member_name}} больше не состоит в команде {{.tenant_name}}. Доступ этого участника к технике и объявлениям закрыт — проверьте, не осталась ли техника без оператора.",
		},
		{
			nt:            notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_ROLE_CHANGED_MANAGER,
			params:        tn,
			consequenceEN: "You can manage its machinery and listings; its members are still managed by the admin.",
			consequenceRU: "Вы можете вести её технику и объявления; участниками по-прежнему управляет администратор.",
			ruTitle:       "Ваша роль изменена",
			ruBody:        "Теперь вы менеджер команды {{.tenant_name}}. Вы можете вести её технику и объявления; участниками по-прежнему управляет администратор.",
		},
		{
			// П-06 / the rights matrix: an operator adds no machinery and runs
			// no listings. The sentence is deliberately negative-only — the
			// vault disagrees with itself on whether an operator reads the
			// whole garage or only assigned units, so claiming either would be
			// copy that contradicts the spec.
			nt:            notificationv1.NotificationType_NOTIFICATION_TYPE_MEMBER_ROLE_CHANGED_OPERATOR,
			params:        tn,
			consequenceEN: "You can no longer add machinery or manage its listings.",
			consequenceRU: "Добавлять технику и вести объявления команды вы больше не можете.",
			ruTitle:       "Ваша роль изменена",
			ruBody:        "Теперь вы оператор команды {{.tenant_name}}. Добавлять технику и вести объявления команды вы больше не можете.",
		},
	}
}

// templateAction matches one {{…}} action, so prose can be inspected without
// the param names — {{.tenant_name}} is a wire contract and must keep saying
// tenant, while the sentence around it must not.
var templateAction = regexp.MustCompile(`\{\{[^}]*\}\}`)

// TestMembershipCopyNeverSaysTenant is the Part-1 guard: no membership
// notification's user-facing prose may carry the backend's word. It walks the
// catalogue's own type list rather than a hand-kept key list, so a new
// membership type cannot slip past it.
func TestMembershipCopyNeverSaysTenant(t *testing.T) {
	cases := membershipCopyCases()
	if len(cases) != 13 {
		t.Fatalf("expected the 13 membership types, got %d — update this file with the new one", len(cases))
	}
	for _, tc := range cases {
		key, ok := typeKey[tc.nt]
		if !ok {
			t.Fatalf("%s has no typeKey entry", tc.nt)
		}
		var pair strings.Builder
		for _, field := range []string{"title", "body"} {
			value, ok := BaselineEN[key+"."+field]
			if !ok {
				t.Fatalf("BaselineEN lacks %s.%s", key, field)
			}
			prose := templateAction.ReplaceAllString(value, " ")
			if strings.Contains(strings.ToLower(prose), "tenant") {
				t.Errorf("%s.%s still says tenant in its prose: %q", key, field, value)
			}
			pair.WriteString(prose + " ")
		}
		// A title may be context-free ("Access restored"), but the pair a user
		// reads must name what they were removed from or invited to.
		if !strings.Contains(strings.ToLower(pair.String()), "team") {
			t.Errorf("%s names no team in title or body: %q / %q",
				key, BaselineEN[key+".title"], BaselineEN[key+".body"])
		}
	}
}

// TestMembershipCopyRendersTheConsequenceFromTheBaseline renders every
// membership type with NO overlay mounted, so what it checks is the compiled
// English baseline reaching a rendered message — the state every environment
// is in before the i18n-catalog ConfigMap lands, and the one a catalogue edit
// cannot repair.
func TestMembershipCopyRendersTheConsequenceFromTheBaseline(t *testing.T) {
	b := i18n.NewOverlayBundle(BaselineEN, "", i18n.WithAllowedParams(AllowedParamsByKey))
	if sum := b.Load(context.Background()); sum.Source != "baselines-only" {
		t.Fatalf("Source = %q, want baselines-only — this test must not read an overlay", sum.Source)
	}
	r := NewRenderer(b)

	for _, tc := range membershipCopyCases() {
		t.Run(tc.nt.String(), func(t *testing.T) {
			title, body, err := r.Render(tc.nt, tc.params, "en")
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			assertRenderedCopy(t, title, body, tc.params, tc.consequenceEN)
		})
	}
}

// TestMembershipCopyRendersTheConsequenceFromTheRussianOverlay mounts the
// i18n-catalog Russian for the same 13 types and proves it renders — same
// clause, same params, nothing left uninterpolated. The two locales must not
// drift in meaning, so the Russian is checked on the rendered message exactly
// as the English is.
func TestMembershipCopyRendersTheConsequenceFromTheRussianOverlay(t *testing.T) {
	cases := membershipCopyCases()

	bundle := make(map[string]map[string]string, len(cases))
	for _, tc := range cases {
		bundle[typeKey[tc.nt]] = map[string]string{"title": tc.ruTitle, "body": tc.ruBody}
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal ru overlay: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ru.json"), raw, 0o600); err != nil {
		t.Fatalf("write ru.json: %v", err)
	}

	b := i18n.NewOverlayBundle(BaselineEN, dir, i18n.WithAllowedParams(AllowedParamsByKey))
	sum := b.Load(context.Background())
	if sum.Source != "overlay" {
		t.Fatalf("Source = %q, want overlay — the catalogue bundle was not read", sum.Source)
	}
	r := NewRenderer(b)

	for _, tc := range cases {
		t.Run(tc.nt.String(), func(t *testing.T) {
			title, body, err := r.Render(tc.nt, tc.params, "ru")
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if strings.Contains(strings.ToLower(templateAction.ReplaceAllString(title+" "+body, " ")), "тенант") {
				t.Errorf("Russian copy still says тенант: %q / %q", title, body)
			}
			assertRenderedCopy(t, title, body, tc.params, tc.consequenceRU)
		})
	}
}

// assertRenderedCopy checks the three properties every rendered membership
// message must have: the consequence clause survived, every param the
// directive carries actually appears (a placeholder renamed out of the prose
// would otherwise render an empty value and go unnoticed), and no action was
// left uninterpolated.
func assertRenderedCopy(t *testing.T, title, body string, params map[string]string, consequence string) {
	t.Helper()
	if !strings.Contains(body, consequence) {
		t.Errorf("body states no consequence\n  got : %q\n  want it to contain: %q", body, consequence)
	}
	whole := title + " " + body
	for name, value := range params {
		if !strings.Contains(whole, value) {
			t.Errorf("param %q (= %q) is interpolated nowhere in %q", name, value, whole)
		}
	}
	if strings.Contains(whole, "{{") || strings.Contains(whole, "}}") {
		t.Errorf("rendered output still carries a template action: %q", whole)
	}
}

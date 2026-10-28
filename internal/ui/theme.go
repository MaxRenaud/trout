package ui

import "github.com/MaxRenaud/trout/internal/state"

type Theme struct {
	Pal   [16]RGB // what mIRC color codes 00-15 render as
	Roles [state.RoleCount]RGB

	Face, FaceText, Light, Shadow  RGB // window chrome and its 3D edges
	Hover, Pressed, ButtonDown     RGB
	TitleA, TitleB, TitleText      RGB // title bar gradient
	MenuSel, MenuSelText, Disabled RGB
	TextBG, TextFG, SelBG, SelFG   RGB // text panes, lists and the editbox
	ShadeBG, Marker                RGB // line shading, the line marker
	Lines                          RGB // tree lines and separators on TextBG
	TrackFG, TrackBG               RGB // the dithered scrollbar track
	ActEvent, ActMsg, ActHi        RGB // switchbar and treebar activity
	Warn                           RGB // errors in dialogs, on Face
}

var themes = []*Theme{&classicTheme, &darkTheme}

// themeNames are the themes as config.toml names them.
var themeNames = []string{"classic", "dark"}

// classicTheme is mIRC's default colors in Windows 98 chrome.
var classicTheme = Theme{
	Pal: [16]RGB{
		0xFFFFFF, 0x000000, 0x00007F, 0x009300, 0xFF0000, 0x7F0000, 0x9C009C, 0xFC7F00,
		0xFFFF00, 0x00FC00, 0x009393, 0x00FFFF, 0x0000FC, 0xFF00FF, 0x7F7F7F, 0xD2D2D2,
	},
	Roles: [state.RoleCount]RGB{
		state.RoleNormal: 0x000000, state.RoleOwn: 0x000000, state.RoleAction: 0x9C009C, state.RoleEvent: 0x009300,
		state.RoleQuit: 0x00007F, state.RoleNotice: 0x7F0000, state.RoleInfo: 0x00007F, state.RoleHighlight: 0xFF0000,
		state.RoleTimestamp: 0x000000,
	},
	Face: 0xC0C0C0, FaceText: 0x000000, Light: 0xFFFFFF, Shadow: 0x808080,
	Hover: 0xD4D4D4, Pressed: 0xA8A8A8, ButtonDown: 0xE4E4E4,
	TitleA: 0x000080, TitleB: 0x1084D0, TitleText: 0xFFFFFF,
	MenuSel: 0x000080, MenuSelText: 0xFFFFFF, Disabled: 0x808080,
	TextBG: 0xFFFFFF, TextFG: 0x000000, SelBG: 0x000080, SelFG: 0xFFFFFF,
	ShadeBG: 0xF0F0F0, Marker: 0xD04040,
	Lines:   0x808080,
	TrackFG: 0xFFFFFF, TrackBG: 0xC0C0C0,
	ActEvent: 0x7F0000, ActMsg: 0xFF0000, ActHi: 0x0000FC,
	Warn: 0x9C0000,
}

// darkTheme keeps the layout but swaps mIRC colors 0 and 1, so black on
// white text art stays readable, and brightens the colors that were dark.
var darkTheme = Theme{
	Pal: [16]RGB{
		0x1E1E1E, 0xE0E0E0, 0x6F8CFF, 0x4EC94E, 0xFF5C5C, 0xD08770, 0xC678DD, 0xFFA040,
		0xFFE066, 0x8AFF8A, 0x3CC8C8, 0x7FFFFF, 0x7AA2FF, 0xFF7AFF, 0x9A9A9A, 0x505050,
	},
	Roles: [state.RoleCount]RGB{
		state.RoleNormal: 0xD4D4D4, state.RoleOwn: 0xF0F0F0, state.RoleAction: 0xC678DD, state.RoleEvent: 0x6CC36C,
		state.RoleQuit: 0x7A9BFF, state.RoleNotice: 0xD19A66, state.RoleInfo: 0x7A9BFF, state.RoleHighlight: 0xFF6B6B,
		state.RoleTimestamp: 0x7A7A7A,
	},
	Face: 0x2D2D30, FaceText: 0xCCCCCC, Light: 0x5A5A60, Shadow: 0x151517,
	Hover: 0x3E3E42, Pressed: 0x1F1F22, ButtonDown: 0x45454D,
	TitleA: 0x1B2A47, TitleB: 0x3A6AB0, TitleText: 0xFFFFFF,
	MenuSel: 0x094771, MenuSelText: 0xFFFFFF, Disabled: 0x6A6A6A,
	TextBG: 0x1E1E1E, TextFG: 0xD4D4D4, SelBG: 0x264F78, SelFG: 0xFFFFFF,
	ShadeBG: 0x262628, Marker: 0xB05050,
	Lines:   0x5A5A5A,
	TrackFG: 0x3A3A3E, TrackBG: 0x252528,
	ActEvent: 0xD19A66, ActMsg: 0xFF6B6B, ActHi: 0x6FA8FF,
	Warn: 0xFF7A7A,
}

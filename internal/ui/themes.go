package ui

import "github.com/MaxRenaud/trout/internal/state"

// More themes, oldest look first. The dark ones swap mIRC colors 0 and 1,
// like darkTheme, so black on white text art stays readable. Palettes
// borrowed from elsewhere say where from.

// phosphorTheme is a green monochrome terminal (P1 phosphor): every color
// is a shade of green, so IRC colors come out as brightness.
var phosphorTheme = Theme{
	Pal: [16]RGB{
		0x050A06, 0xB6FFC8, 0x1A7A36, 0x2FD15A, 0x9CFFB4, 0x1F8A3E, 0x3BBF60, 0x7DFF9E,
		0xC8FFD4, 0x5CFF85, 0x27A84A, 0x8CFFAA, 0x33C25A, 0x99FFB3, 0x1E6B30, 0x4FD872,
	},
	Roles: [state.RoleCount]RGB{
		state.RoleNormal: 0x33FF66, state.RoleOwn: 0x7DFF9E, state.RoleAction: 0x5CE07F, state.RoleEvent: 0x1FAF4A,
		state.RoleQuit: 0x1A9440, state.RoleNotice: 0x66FF99, state.RoleInfo: 0x1FAF4A, state.RoleHighlight: 0xD0FFD8,
		state.RoleTimestamp: 0x1A8A3C,
	},
	Face: 0x0F2414, FaceText: 0x33FF66, Light: 0x2E7A3E, Shadow: 0x020602,
	Hover: 0x1A3D22, Pressed: 0x081409, ButtonDown: 0x22502C,
	TitleA: 0x0D3B1A, TitleB: 0x1E7A36, TitleText: 0xB6FFC8,
	MenuSel: 0x33FF66, MenuSelText: 0x031A08, Disabled: 0x1E6B30,
	TextBG: 0x050A06, TextFG: 0x33FF66, SelBG: 0x33FF66, SelFG: 0x031A08,
	ShadeBG: 0x0A140C, Marker: 0x7DFF9E,
	Lines:   0x1E6B30,
	TrackFG: 0x1E6B30, TrackBG: 0x0A140C,
	ActEvent: 0x1FAF4A, ActMsg: 0x7DFF9E, ActHi: 0xD0FFD8,
	Warn: 0xB6FFC8,
}

// amberTheme is the amber monochrome terminal (P3 phosphor).
var amberTheme = Theme{
	Pal: [16]RGB{
		0x0D0800, 0xFFE8B8, 0x8A5A00, 0xE09A00, 0xFFD580, 0x9A6400, 0xC88400, 0xFFC340,
		0xFFF0C8, 0xFFCC55, 0xA86E00, 0xFFDC90, 0xD08A00, 0xFFCF7A, 0x6E4800, 0xE0A030,
	},
	Roles: [state.RoleCount]RGB{
		state.RoleNormal: 0xFFB000, state.RoleOwn: 0xFFD580, state.RoleAction: 0xFFC340, state.RoleEvent: 0xB87800,
		state.RoleQuit: 0x9A6400, state.RoleNotice: 0xFFCC55, state.RoleInfo: 0xB87800, state.RoleHighlight: 0xFFF0C8,
		state.RoleTimestamp: 0x8A5A00,
	},
	Face: 0x2A1A00, FaceText: 0xFFB000, Light: 0x7A5200, Shadow: 0x060300,
	Hover: 0x3D2700, Pressed: 0x140C00, ButtonDown: 0x4D3300,
	TitleA: 0x3D2600, TitleB: 0x8A5A00, TitleText: 0xFFE8B8,
	MenuSel: 0xFFB000, MenuSelText: 0x1A1000, Disabled: 0x6E4800,
	TextBG: 0x0D0800, TextFG: 0xFFB000, SelBG: 0xFFB000, SelFG: 0x1A1000,
	ShadeBG: 0x181000, Marker: 0xFFD580,
	Lines:   0x6E4800,
	TrackFG: 0x6E4800, TrackBG: 0x181000,
	ActEvent: 0xB87800, ActMsg: 0xFFD580, ActHi: 0xFFF0C8,
	Warn: 0xFFE8B8,
}

// dosTheme is a late-80s text-mode program: CGA colors, blue panes, grey
// menus with a green selection, a flat title.
var dosTheme = Theme{
	Pal: [16]RGB{
		0xFFFFFF, 0x000000, 0x5555FF, 0x00AA00, 0xFF5555, 0xAA5500, 0xAA00AA, 0xFFAA55,
		0xFFFF55, 0x55FF55, 0x00AAAA, 0x55FFFF, 0x5555FF, 0xFF55FF, 0x555555, 0xAAAAAA,
	},
	Roles: [state.RoleCount]RGB{
		state.RoleNormal: 0xAAAAAA, state.RoleOwn: 0xFFFFFF, state.RoleAction: 0xFF55FF, state.RoleEvent: 0x55FF55,
		state.RoleQuit: 0x55FFFF, state.RoleNotice: 0xFFFF55, state.RoleInfo: 0x55FFFF, state.RoleHighlight: 0xFF5555,
		state.RoleTimestamp: 0x00AAAA,
	},
	Face: 0xAAAAAA, FaceText: 0x000000, Light: 0xFFFFFF, Shadow: 0x555555,
	Hover: 0x00AAAA, Pressed: 0x008888, ButtonDown: 0x55FFFF,
	TitleA: 0x0000AA, TitleB: 0x0000AA, TitleText: 0xFFFF55,
	MenuSel: 0x00AA00, MenuSelText: 0x000000, Disabled: 0x555555,
	TextBG: 0x0000AA, TextFG: 0xAAAAAA, SelBG: 0x00AAAA, SelFG: 0x000000,
	ShadeBG: 0x000090, Marker: 0xFF5555,
	Lines:   0x00AAAA,
	TrackFG: 0x00AAAA, TrackBG: 0x000088,
	ActEvent: 0xAA0000, ActMsg: 0xFF5555, ActHi: 0x0000AA,
	Warn: 0xAA0000,
}

// bbsTheme is a dial-up BBS in ANSI art: CGA colors on black, cyan
// chrome, magenta and yellow.
var bbsTheme = Theme{
	Pal: [16]RGB{
		0x000000, 0xFFFFFF, 0x0000AA, 0x00AA00, 0xFF5555, 0xAA5500, 0xAA00AA, 0xFFAA55,
		0xFFFF55, 0x55FF55, 0x00AAAA, 0x55FFFF, 0x5555FF, 0xFF55FF, 0x555555, 0xAAAAAA,
	},
	Roles: [state.RoleCount]RGB{
		state.RoleNormal: 0xAAAAAA, state.RoleOwn: 0xFFFFFF, state.RoleAction: 0xFF55FF, state.RoleEvent: 0x55FF55,
		state.RoleQuit: 0x5555FF, state.RoleNotice: 0xFFFF55, state.RoleInfo: 0x55FFFF, state.RoleHighlight: 0xFF5555,
		state.RoleTimestamp: 0x555555,
	},
	Face: 0x00AAAA, FaceText: 0x000000, Light: 0x55FFFF, Shadow: 0x005555,
	Hover: 0x55FFFF, Pressed: 0x008888, ButtonDown: 0x55FFFF,
	TitleA: 0xAA00AA, TitleB: 0xFF55FF, TitleText: 0xFFFF55,
	MenuSel: 0xAA00AA, MenuSelText: 0xFFFF55, Disabled: 0x005555,
	TextBG: 0x000000, TextFG: 0xAAAAAA, SelBG: 0xAA00AA, SelFG: 0xFFFF55,
	ShadeBG: 0x111111, Marker: 0xFF5555,
	Lines:   0x00AAAA,
	TrackFG: 0x00AAAA, TrackBG: 0x000000,
	ActEvent: 0x0000AA, ActMsg: 0xAA0000, ActHi: 0xAA00AA,
	Warn: 0xAA0000,
}

// contrastTheme is Windows' High Contrast Black: white on black, white
// edges, cyan selection, yellow and green accents.
var contrastTheme = Theme{
	Pal: [16]RGB{
		0x000000, 0xFFFFFF, 0x6FA0FF, 0x3FF23F, 0xFF5050, 0xD08040, 0xFF66FF, 0xFFA500,
		0xFFFF00, 0x80FF80, 0x00E0E0, 0x1AEBFF, 0x5C8CFF, 0xFF80FF, 0xA0A0A0, 0xD0D0D0,
	},
	Roles: [state.RoleCount]RGB{
		state.RoleNormal: 0xFFFFFF, state.RoleOwn: 0xFFFFFF, state.RoleAction: 0xFF66FF, state.RoleEvent: 0x3FF23F,
		state.RoleQuit: 0x1AEBFF, state.RoleNotice: 0xFFFF00, state.RoleInfo: 0x1AEBFF, state.RoleHighlight: 0xFF5050,
		state.RoleTimestamp: 0xC0C0C0,
	},
	Face: 0x000000, FaceText: 0xFFFFFF, Light: 0xFFFFFF, Shadow: 0xFFFFFF,
	Hover: 0x303030, Pressed: 0x000000, ButtonDown: 0x404040,
	TitleA: 0x800080, TitleB: 0x800080, TitleText: 0xFFFFFF,
	MenuSel: 0x1AEBFF, MenuSelText: 0x000000, Disabled: 0x3FF23F,
	TextBG: 0x000000, TextFG: 0xFFFFFF, SelBG: 0x1AEBFF, SelFG: 0x000000,
	ShadeBG: 0x141414, Marker: 0xFFFF00,
	Lines:   0xFFFFFF,
	TrackFG: 0xFFFFFF, TrackBG: 0x000000,
	ActEvent: 0x3FF23F, ActMsg: 0xFFFF00, ActHi: 0x1AEBFF,
	Warn: 0xFFFF00,
}

// solarizedTheme uses Solarized's dark palette (Ethan Schoonover, MIT).
var solarizedTheme = Theme{
	Pal: [16]RGB{
		0x002B36, 0xEEE8D5, 0x268BD2, 0x859900, 0xDC322F, 0xB5652A, 0x6C71C4, 0xCB4B16,
		0xB58900, 0x9FB71F, 0x2AA198, 0x5FD7CC, 0x4FA3E0, 0xD33682, 0x586E75, 0x93A1A1,
	},
	Roles: [state.RoleCount]RGB{
		state.RoleNormal: 0x839496, state.RoleOwn: 0x93A1A1, state.RoleAction: 0xD33682, state.RoleEvent: 0x859900,
		state.RoleQuit: 0x268BD2, state.RoleNotice: 0xCB4B16, state.RoleInfo: 0x2AA198, state.RoleHighlight: 0xDC322F,
		state.RoleTimestamp: 0x586E75,
	},
	Face: 0x073642, FaceText: 0x93A1A1, Light: 0x1E5260, Shadow: 0x001A21,
	Hover: 0x0E4553, Pressed: 0x002B36, ButtonDown: 0x14505F,
	TitleA: 0x073642, TitleB: 0x268BD2, TitleText: 0xFDF6E3,
	MenuSel: 0x268BD2, MenuSelText: 0xFDF6E3, Disabled: 0x586E75,
	TextBG: 0x002B36, TextFG: 0x839496, SelBG: 0x268BD2, SelFG: 0xFDF6E3,
	ShadeBG: 0x01313D, Marker: 0xDC322F,
	Lines:   0x586E75,
	TrackFG: 0x0E4553, TrackBG: 0x01313D,
	ActEvent: 0xCB4B16, ActMsg: 0xDC322F, ActHi: 0x268BD2,
	Warn: 0xDC322F,
}

// gruvboxTheme uses gruvbox's dark palette (Pavel Pertsev, MIT).
var gruvboxTheme = Theme{
	Pal: [16]RGB{
		0x282828, 0xFBF1C7, 0x458588, 0x98971A, 0xFB4934, 0xAF3A03, 0xB16286, 0xFE8019,
		0xFABD2F, 0xB8BB26, 0x689D6A, 0x8EC07C, 0x83A598, 0xD3869B, 0x928374, 0xA89984,
	},
	Roles: [state.RoleCount]RGB{
		state.RoleNormal: 0xEBDBB2, state.RoleOwn: 0xFBF1C7, state.RoleAction: 0xD3869B, state.RoleEvent: 0xB8BB26,
		state.RoleQuit: 0x83A598, state.RoleNotice: 0xFE8019, state.RoleInfo: 0x8EC07C, state.RoleHighlight: 0xFB4934,
		state.RoleTimestamp: 0x928374,
	},
	Face: 0x3C3836, FaceText: 0xEBDBB2, Light: 0x665C54, Shadow: 0x1D2021,
	Hover: 0x504945, Pressed: 0x32302F, ButtonDown: 0x504945,
	TitleA: 0x3C3836, TitleB: 0xD65D0E, TitleText: 0xFBF1C7,
	MenuSel: 0x458588, MenuSelText: 0xFBF1C7, Disabled: 0x7C6F64,
	TextBG: 0x282828, TextFG: 0xEBDBB2, SelBG: 0x458588, SelFG: 0xFBF1C7,
	ShadeBG: 0x32302F, Marker: 0xFB4934,
	Lines:   0x665C54,
	TrackFG: 0x504945, TrackBG: 0x32302F,
	ActEvent: 0xFE8019, ActMsg: 0xFB4934, ActHi: 0x83A598,
	Warn: 0xFB4934,
}

// nordTheme uses Nord's palette (Arctic Ice Studio, MIT).
var nordTheme = Theme{
	Pal: [16]RGB{
		0x2E3440, 0xECEFF4, 0x5E81AC, 0xA3BE8C, 0xBF616A, 0x9A6A5A, 0xB48EAD, 0xD08770,
		0xEBCB8B, 0xB9D6A0, 0x8FBCBB, 0x88C0D0, 0x81A1C1, 0xD4A0C8, 0x616E88, 0xAEB6C4,
	},
	Roles: [state.RoleCount]RGB{
		state.RoleNormal: 0xD8DEE9, state.RoleOwn: 0xECEFF4, state.RoleAction: 0xB48EAD, state.RoleEvent: 0xA3BE8C,
		state.RoleQuit: 0x81A1C1, state.RoleNotice: 0xD08770, state.RoleInfo: 0x88C0D0, state.RoleHighlight: 0xBF616A,
		state.RoleTimestamp: 0x616E88,
	},
	Face: 0x3B4252, FaceText: 0xE5E9F0, Light: 0x4C566A, Shadow: 0x242933,
	Hover: 0x434C5E, Pressed: 0x2E3440, ButtonDown: 0x4C566A,
	TitleA: 0x3B4252, TitleB: 0x5E81AC, TitleText: 0xECEFF4,
	MenuSel: 0x5E81AC, MenuSelText: 0xECEFF4, Disabled: 0x616E88,
	TextBG: 0x2E3440, TextFG: 0xD8DEE9, SelBG: 0x5E81AC, SelFG: 0xECEFF4,
	ShadeBG: 0x323946, Marker: 0xBF616A,
	Lines:   0x4C566A,
	TrackFG: 0x434C5E, TrackBG: 0x323946,
	ActEvent: 0xD08770, ActMsg: 0xBF616A, ActHi: 0x88C0D0,
	Warn: 0xBF616A,
}

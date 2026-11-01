# Themes

trout has ten themes, in View > Themes and in the Scheme list of Tools > Colors:

| Theme | Name in files | The look |
|---|---|---|
| Classic | `classic` | mIRC's own colors in Windows 98 chrome; the default |
| Dark | `dark` | code-editor greys, mIRC colors 0 and 1 swapped so text art stays readable |
| Green Phosphor | `phosphor` | a green monochrome terminal: IRC colors become shades of green |
| Amber | `amber` | the amber monochrome terminal |
| DOS Blue | `dos` | a late-80s text-mode program: blue panes, grey menus, a green selection |
| BBS | `bbs` | a dial-up BBS in ANSI art: CGA colors on black, cyan chrome |
| High Contrast | `contrast` | Windows' High Contrast Black |
| Solarized | `solarized` | Solarized's dark palette |
| Gruvbox | `gruvbox` | gruvbox's dark palette |
| Nord | `nord` | Nord's palette |

F2 switches between Classic and Dark. The theme you pick is saved in `config.toml` by its
name, as `theme = "nord"`.

The Colors dialog works on top of the theme, like mIRC's: it gives any kind of text (or
the background) one of the theme's 16 mIRC colors, and Reset goes back to the theme's.

## Your own themes

A theme file is a `.toml` file in the `themes` folder next to `config.toml`:

- macOS: `~/Library/Application Support/trout/themes/`
- Linux: `~/.config/trout/themes/`
- Windows: `%AppData%\trout\themes\`

trout reads them when it starts, so restart it after changing one. The file's name is the
theme's name (`paper.toml` is `paper`), and it can't be a built-in theme's. If something
in a file is wrong, the status window says what, and the theme is still there with the
colors that were right.

A theme starts from a built-in one (`base`, Classic if you leave it out) and changes the
colors it lists. Colors are `#RRGGBB`.

```toml
label = "Paper"     # in View > Themes; the file's name if you leave it out
base = "classic"    # the built-in theme it starts from

# mIRC colors 00-15, what color codes in messages show as: all 16, or leave it out.
palette = [
  "#FFFFFF", "#000000", "#00007F", "#009300", "#FF0000", "#7F0000", "#9C009C", "#FC7F00",
  "#FFFF00", "#00FC00", "#009393", "#00FFFF", "#0000FC", "#FF00FF", "#7F7F7F", "#D2D2D2",
]

[colors]
text-bg = "#F7F3E8"
text-fg = "#222222"
normal = "#222222"
shade-bg = "#EFEADC"
```

### The colors

Text in windows:

| Key | What |
|---|---|
| `normal` | messages |
| `own` | your messages |
| `action` | `/me` |
| `event` | joins, parts, modes |
| `quit` | quits |
| `notice` | notices |
| `info` | trout's own lines |
| `highlight` | lines that mention you |
| `timestamp` | timestamps |

The text panes (windows, lists, the editbox, the treebar and nicklist):

| Key | What |
|---|---|
| `text-bg`, `text-fg` | background and plain text |
| `sel-bg`, `sel-fg` | the selection |
| `shade-bg` | line shading (Options > Display) |
| `marker` | the line marker |
| `lines` | tree lines and separators |
| `track-fg`, `track-bg` | the scrollbar's dotted track |

Window chrome (menus, dialogs, the toolbar and switchbar):

| Key | What |
|---|---|
| `face`, `face-text` | the grey of Windows 98, and text on it |
| `light`, `shadow` | the raised edges' light and dark sides |
| `hover`, `pressed`, `button-down` | buttons under the mouse, pressed, and held down |
| `title-a`, `title-b`, `title-text` | the title bar's gradient, left to right, and its text |
| `menu-sel`, `menu-sel-text` | the highlighted menu item |
| `disabled` | greyed-out items |
| `act-event`, `act-message`, `act-highlight` | switchbar and treebar activity |
| `warn` | errors in dialogs |

# Scripting: an alias and an event typed into the Scripts Editor's
# Remote tab, saved, then used in #go-nuts.
Output docs/gifs/scripts.gif
Source dev/vhs/settings.tape

Hide
Type "clear; ./trout --demo"
Enter
Sleep 2s
Show

Sleep 1.5s
Alt+r
Sleep 1.5s
Set TypingSpeed 35ms
Type "alias hello say Hello $1 $+ ! Welcome to $active ( $+ $nick($active,0) of us)"
Enter
Type "on *:INPUT:#:if ($1 == !time) { say It's $time here | halt }"
Sleep 1.2s
Ctrl+S
Sleep 1s
Escape
Sleep 800ms
Set TypingSpeed 55ms
Type "/hello alice"
Sleep 600ms
Enter
Sleep 1.5s
Type "!time"
Sleep 600ms
Enter
Sleep 3s

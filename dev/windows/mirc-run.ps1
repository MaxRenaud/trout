# Runs the test copy of mIRC in trout-oracle\ on this desktop (it needs a
# desktop session, so dev/windows/mirc-run starts this as a scheduled task)
# and waits for its oracle.mrc to exit. It writes status.txt when done.
$d = "$env:USERPROFILE\trout-oracle"
Remove-Item "$d\status.txt" -ErrorAction SilentlyContinue
$p = Start-Process "$d\mirc.exe" -ArgumentList "-r`"$d`"", "-noconnect" -PassThru
if ($p.WaitForExit(60000)) {
	"exited" | Set-Content "$d\status.txt"
} else {
	Stop-Process -Id $p.Id -Force
	"timeout" | Set-Content "$d\status.txt"
}

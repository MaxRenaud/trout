package script

import "strings"

// mircIdents are the identifiers mIRC has, from its help file (7.86). An
// identifier that isn't built in or an alias gives $null, like mIRC, unless
// it's one of these: then it's an error, since the script expects it to
// work and trout doesn't support it (yet).
var mircIdents = map[string]bool{}

func init() {
	for name := range strings.FieldsSeq(mircIdentList) {
		mircIdents[name] = true
	}
}

const mircIdentList = "" +
	"abook abs acos active activewid address addtok addtokcs agent agentname agentstat " +
	"agentver alias and anick ansi2mirc appactive appstate argon2 asc asctime asin atan away " +
	"base bfind bindip bitoff biton bvar bytes calc caller cb cbrt ceil chan chanmodes " +
	"chantypes chat chr cid clevel click cmdbox cmdline cnick codepage color com comcall " +
	"comchan comchar comerr compact compress comval cos cosh count countcs cr crc creq crlf " +
	"ctime ctimer darkmode date day daylight dccignore dccport dde ddename decode decompress " +
	"deltok dialog did didreg didtok didwm disk dlevel dll dllcall dns dqwindow duration " +
	"ebeeps editbox emailaddr encode envvar eval event eventid eventparms exists exiting " +
	"feof fgetc file filtered finddir findfile findtok findtokcs fline floor font fopen " +
	"fread fromeditbox fserve fulladdress fulldate fullname fullscreen gcd get getdir getdot " +
	"gettok gmt group hash height hfind hget highlight hmac host hotp hypot ial ialchan " +
	"ialmark ibl idle iel ifmatch ignore iif iil inellipse ini inpaste inpoly input inrect " +
	"inroundrect insong instok int intersect inwave ip iptype iql isadmin isalias isbit " +
	"isdde isdir isfile isid islower isnum istok istokcs isupper keychar keylparam keyrpt " +
	"keyval lactive lactivewid lcm left len level lf line lines link lock locked log logdir " +
	"logstamp logstampfmt longfn longip lower ltimer maddress mask matchkey matchtok " +
	"matchtokcs max maxlens md5 me mid mididir min mircdir mircexe mircini mircpid mklogfn " +
	"mnick mode modespl mouse msfile msgstamp msgtags network nick nickmode nofile nopath " +
	"noqt not notags notify numeric numtok online onpoly or ord os parms passivedcc pbkdf2 " +
	"pi pic port portable portfree pos poscs powmod prefix puttok qt query rand rawbytes " +
	"rawmsg read readini readn regerrstr regex regml regmlex regsub regsubex remote remove " +
	"removecs remtok remtokcs replace replacecs replacex replacexcs reptok reptokcs result " +
	"rgb right round samepath scid scon script scriptdir scriptline sdir send server " +
	"serverip servertarget sfile sfstate sha1 shortfn show sin sinh site sline snick snicks " +
	"snotify sock sockbr sockerr sockname sorttok sorttokcs sound sqrt sreq ssl sslcertvalid " +
	"sslhash sslready sslversion starting status str strip stripped submenu sysdir tan tanh " +
	"target tempfn ticks ticksqpc time timer timestamp timestampfmt timezone tip tips " +
	"titlebar toolbar totp trust ulevel ulist unsafe upper uptime url urlget usermode " +
	"utfencode v1 var vcmd vcmdstat vcmdver version vol wid width wildsite wildtok wildtokcs " +
	"window wrap xor zip"

package script

// Mask is $mask(address, type) for the client: nick!user@host as one of
// mIRC's mask types, 0 to 9, for commands that take one (/ignore nick 1).
// It's "" if address isn't a full address.
func Mask(address string, kind int) (string, error) { return mask(address, kind) }

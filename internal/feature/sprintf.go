package feature

import "fmt"

// sprintf : alias local, pour que colibri.go reste lisible dans la
// construction des noms de colonnes.
func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

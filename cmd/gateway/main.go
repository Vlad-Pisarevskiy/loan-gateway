// Command gateway — шлюз заявок на кредит. Подкоманды: api, worker, migrate.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "usage: gateway api|worker|migrate")
	os.Exit(2)
}

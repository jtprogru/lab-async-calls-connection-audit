package inventory

import (
	"bufio"
	"fmt"
	"net"
)

// Reserve резервирует товар на складе по текстовому протоколу.
func Reserve(sku string, qty int) error {
	conn, err := net.Dial("tcp", "inventory.internal:7000")
	if err != nil {
		return err
	}
	fmt.Fprintf(conn, "RESERVE %s %d\n", sku, qty)
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return err
	}
	if line != "OK\n" {
		return fmt.Errorf("inventory: %s", line)
	}
	return nil
}

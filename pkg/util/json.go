package util

import (
	"encoding/json"
	"fmt"
)

func PrintJSON(obj any) {
	txt, _ := json.MarshalIndent(obj, "", "  ")
	fmt.Println(string(txt))
}

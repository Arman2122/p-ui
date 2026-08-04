package middleware

import (
	"github.com/go-playground/validator/v10"

	"github.com/Arman2122/p-ui/internal/cores"
)

/*
`protocol` accepts exactly the protocols a registered core can serve.

It replaces a hand-typed `oneof=` list, which is what let the accepted set drift
from the servable one. Rejecting here is the honest answer: an inbound the panel
stores but no core claims is one it will refuse to apply for as long as it exists.
*/
func registerProtocolRule() {
	kinds := cores.Kinds()
	served := make(map[string]bool, len(kinds))
	for _, kind := range kinds {
		served[string(kind)] = true
	}
	err := validate.RegisterValidation("protocol", func(fl validator.FieldLevel) bool {
		return served[fl.Field().String()]
	})
	if err != nil {
		panic("middleware: registering the protocol rule: " + err.Error())
	}
}

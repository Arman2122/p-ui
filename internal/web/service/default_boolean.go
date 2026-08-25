package service

import (
	"gorm.io/gorm"
)

/*
applyStagedOff writes back a boolean the INSERT dropped.

GORM omits a zero-valued field whose column carries a `default`, so a row
created with its enable switch off is stored ON. Measured, and no call-level
form overrides it: Select("*"), Select(the column), Omit(the column) and
CreateInBatches all still store the default (audit-appendix ledger L9).

So the only way to say false in the struct-create shape is to say it again, in a
second statement. Callers pass the value they were given; this is a no-op for
every row that was staying on, which is nearly all of them.
*/
func applyStagedOff(tx *gorm.DB, model any, column string, enabled bool, ids ...int) error {
	if enabled || len(ids) == 0 {
		return nil
	}
	return tx.Model(model).Where("id IN ?", ids).Update(column, false).Error
}

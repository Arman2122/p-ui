package session

import (
	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func IsAdminUser(user *model.User) bool {
	return user != nil && (user.Role == "" || user.Role == "admin")
}

func IsAdmin(c *gin.Context) bool {
	return IsAdminUser(GetLoginUser(c))
}

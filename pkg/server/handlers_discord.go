package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lucasduport/iptv-proxy/pkg/types"
	"github.com/lucasduport/iptv-proxy/pkg/utils"
)

// linkDiscordUser links a Discord user ID to an LDAP username
func (c *Config) linkDiscordUser(ctx *gin.Context) {
	utils.DebugLog("API: Request to link Discord user to LDAP")

	var req struct {
		DiscordID   string `json:"discord_id"`
		DiscordName string `json:"discord_name"`
		LDAPUser    string `json:"ldap_user"`
		Token       string `json:"token"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		utils.ErrorLog("API: Invalid Discord link request: %v", err)
		ctx.JSON(http.StatusBadRequest, types.APIResponse{
			Success: false,
			Error:   "Invalid request: " + err.Error(),
		})
		return
	}

	utils.DebugLog("API: Linking Discord ID %s (%s) to LDAP user %s", req.DiscordID, req.DiscordName, req.LDAPUser)

	if c.db == nil {
		utils.ErrorLog("Database is nil in linkDiscordUser")
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Database not initialized",
		})
		return
	}

	if err := c.db.LinkDiscordToLDAP(req.DiscordID, req.DiscordName, req.LDAPUser); err != nil {
		utils.ErrorLog("API: Failed to link Discord to LDAP: %v", err)
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Failed to link accounts: " + err.Error(),
		})
		return
	}

	utils.InfoLog("Successfully linked Discord ID %s (%s) to LDAP user %s", req.DiscordID, req.DiscordName, req.LDAPUser)

	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Message: "Discord account linked successfully",
		Data: map[string]string{
			"discord_id":   req.DiscordID,
			"discord_name": req.DiscordName,
			"ldap_user":    req.LDAPUser,
		},
	})
}

// getLDAPFromDiscord gets the LDAP username for a Discord ID
func (c *Config) getLDAPFromDiscord(ctx *gin.Context) {
	discordID := ctx.Param("discordid")
	utils.DebugLog("API: Getting LDAP user for Discord ID: %s", discordID)

	if c.db == nil {
		utils.ErrorLog("Database is nil in getLDAPFromDiscord")
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Database not initialized",
		})
		return
	}

	ldapUser, err := c.db.GetLDAPUserByDiscordID(discordID)
	if err != nil {
		utils.DebugLog("API: Discord user not linked: %v", err)
		ctx.JSON(http.StatusNotFound, types.APIResponse{
			Success: false,
			Error:   "Discord user not linked: " + err.Error(),
		})
		return
	}

	utils.DebugLog("API: Found LDAP user %s for Discord ID %s", ldapUser, discordID)
	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Data: map[string]string{
			"ldap_user": ldapUser,
		},
	})
}

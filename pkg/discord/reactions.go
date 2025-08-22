package discord

import (
    "github.com/bwmarrin/discordgo"
    "github.com/lucasduport/stream-share/pkg/utils"
)

// messageReactionAdd handles digit reactions for legacy selection messages.
func (b *Bot) messageReactionAdd(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
    if r.UserID == s.State.User.ID { return }

    b.pendingMsgLock.RLock()
    ctx, ok := b.pendingVODByMsg[r.MessageID]
    b.pendingMsgLock.RUnlock()
    if !ok { return }
    if ctx.UserID != r.UserID { return }

    emojiToIndex := map[string]int{"1️⃣":1, "2️⃣":2, "3️⃣":3, "4️⃣":4, "5️⃣":5, "6️⃣":6, "7️⃣":7, "8️⃣":8, "9️⃣":9, "0️⃣":10}
    selection, ok := emojiToIndex[r.Emoji.Name]
    if !ok { return }

    selected, exists := ctx.Choices[selection]
    if !exists || selected.StreamID == "" {
        utils.WarnLog("Discord: invalid selection via reaction: %d", selection)
        return
    }

    b.pendingMsgLock.Lock()
    delete(b.pendingVODByMsg, r.MessageID)
    b.pendingMsgLock.Unlock()

    b.startVODDownloadFromSelection(s, ctx.ChannelID, r.UserID, selected)
}

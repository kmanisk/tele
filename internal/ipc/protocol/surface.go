package protocol

import "github.com/sorokin-vladimir/tele/internal/core"

// surface is every method of core.Surface, each named and taken as a method
// expression: a function whose first argument is the surface it is called on.
//
// It is written out rather than read off the interface by reflection on
// purpose. A method looked up by reflection with an index or a name the linker
// cannot see - reflect.Type.Method, reflect.Value.Method, MethodByName - makes
// the linker keep every exported method of every type in the binary, and with
// the thousands of generated Telegram types that doubles tele's size (#208).
// A test holds this list to the interface, so a method added there and not
// here fails it.
var surface = map[string]any{
	"Subscribe":        core.Surface.Subscribe,
	"MoveWindow":       core.Surface.MoveWindow,
	"Unsubscribe":      core.Surface.Unsubscribe,
	"LogOutPreview":    core.Surface.LogOutPreview,
	"LogOut":           core.Surface.LogOut,
	"EndAccount":       core.Surface.EndAccount,
	"AnswerLogin":      core.Surface.AnswerLogin,
	"SetMuted":         core.Surface.SetMuted,
	"SetArchived":      core.Surface.SetArchived,
	"SetUnreadMark":    core.Surface.SetUnreadMark,
	"AddToFolder":      core.Surface.AddToFolder,
	"MarkRead":         core.Surface.MarkRead,
	"SetFocus":         core.Surface.SetFocus,
	"ReadReactions":    core.Surface.ReadReactions,
	"ReadMentions":     core.Surface.ReadMentions,
	"EditMessage":      core.Surface.EditMessage,
	"DeleteMessages":   core.Surface.DeleteMessages,
	"SendReaction":     core.Surface.SendReaction,
	"Forward":          core.Surface.Forward,
	"SetTyping":        core.Surface.SetTyping,
	"SaveDraft":        core.Surface.SaveDraft,
	"Send":             core.Surface.Send,
	"SendMedia":        core.Surface.SendMedia,
	"RetryOutbox":      core.Surface.RetryOutbox,
	"DiscardOutbox":    core.Surface.DiscardOutbox,
	"SearchContacts":   core.Surface.SearchContacts,
	"Chats":            core.Surface.Chats,
	"Topics":           core.Surface.Topics,
	"Chat":             core.Surface.Chat,
	"FolderFilters":    core.Surface.FolderFilters,
	"GetParticipants":  core.Surface.GetParticipants,
	"KnownUser":        core.Surface.KnownUser,
	"GetUser":          core.Surface.GetUser,
	"FetchMedia":       core.Surface.FetchMedia,
	"SaveMedia":        core.Surface.SaveMedia,
	"OpenMediaStream":  core.Surface.OpenMediaStream,
	"CloseMediaStream": core.Surface.CloseMediaStream,
	"InvalidateMedia":  core.Surface.InvalidateMedia,
	"FetchAvatar":      core.Surface.FetchAvatar,
	"InvalidateAvatar": core.Surface.InvalidateAvatar,
}

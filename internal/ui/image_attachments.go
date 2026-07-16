package ui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/imageasset"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/safeio"
)

const (
	maxPendingImages           = 4
	maxConversationImages      = 12
	maxConversationImageBytes  = 40 << 20
	maxConversationImagePixels = 48_000_000
)

var (
	errImageAttachmentBusy      = errors.New("another image attachment is still being validated")
	errImageStoreUnavailable    = errors.New("private image storage is unavailable")
	errPendingImageLimitReached = errors.New("pending image limit reached")
)

// pendingImageAttachment keeps the provider payload paired with its durable,
// path-free reference. Raw bytes never enter ChatEntry or session JSON.
type pendingImageAttachment struct {
	Ref   imageasset.Ref
	Image llm.ImageData
}

// ImageAttachmentResultMsg completes one tokened asynchronous image admission.
// Fallback is the original terminal paste; it is restored through the normal
// paste-review path when a path-shaped paste is not a valid image.
type ImageAttachmentResultMsg struct {
	Token     uint64
	Preflight bool
	Name      string
	Ref       imageasset.Ref
	Image     llm.ImageData
	Fallback  string
	Err       error
}

// SetImageStore installs the private attachment store and the Agent resolver
// used to rehydrate path-free image references after session/checkpoint restore.
// It must be called before the Bubble Tea program starts.
func (m *Model) SetImageStore(store *imageasset.Store) {
	m.imageStore = store
	if m.agent == nil {
		return
	}
	if store == nil {
		m.agent.SetImageResolver(nil)
		return
	}
	m.agent.SetImageResolver(func(ctx context.Context, image llm.ImageData) ([]byte, error) {
		return store.Load(ctx, imageasset.Ref{
			Digest: image.SHA256, MIMEType: image.MediaType, Name: image.Name,
			SizeBytes: image.Size, Width: image.Width, Height: image.Height,
		})
	})
}

func (m *Model) beginImageFileAttachment(path, fallback string) tea.Cmd {
	if m.imageAttachRunning {
		token := m.imageAttachToken
		return func() tea.Msg {
			return ImageAttachmentResultMsg{Token: token, Preflight: true, Name: llm.SanitizeImageName(path), Err: errImageAttachmentBusy}
		}
	}
	m.imageAttachToken++
	token := m.imageAttachToken
	displayName := llm.SanitizeImageName(path)
	if m.imageStore == nil {
		return func() tea.Msg {
			return ImageAttachmentResultMsg{Token: token, Preflight: true, Name: displayName, Fallback: fallback, Err: errImageStoreUnavailable}
		}
	}
	if len(m.pendingImages) >= maxPendingImages {
		return func() tea.Msg {
			return ImageAttachmentResultMsg{Token: token, Preflight: true, Name: displayName, Fallback: fallback, Err: errPendingImageLimitReached}
		}
	}
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) && m.agent != nil && strings.TrimSpace(m.agent.WorkDir()) != "" {
		path = filepath.Join(m.agent.WorkDir(), path)
	}
	if m.imageAttachCancel != nil {
		m.imageAttachCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	m.imageAttachCancel = cancel
	m.imageAttachRunning = true
	m.imageAttachFallback = fallback
	m.input.Blur()
	store := m.imageStore
	return tea.Batch(m.startActivityCmd(), func() tea.Msg {
		ref, err := store.AdmitFile(ctx, path)
		if err != nil {
			return ImageAttachmentResultMsg{Token: token, Name: displayName, Fallback: fallback, Err: err}
		}
		image := llm.ImageData{
			SHA256: ref.Digest, Name: ref.Name, MediaType: ref.MIMEType,
			Size: ref.SizeBytes, Width: ref.Width, Height: ref.Height,
		}
		if err := image.ValidateReference(); err != nil {
			return ImageAttachmentResultMsg{Token: token, Name: ref.Name, Fallback: fallback, Err: err}
		}
		return ImageAttachmentResultMsg{Token: token, Name: ref.Name, Ref: ref, Image: image, Fallback: fallback}
	})
}

func (m *Model) beginImageBytesAttachment(name string, data []byte) tea.Cmd {
	if m.imageAttachRunning {
		token := m.imageAttachToken
		return func() tea.Msg {
			return ImageAttachmentResultMsg{Token: token, Preflight: true, Name: llm.SanitizeImageName(name), Err: errImageAttachmentBusy}
		}
	}
	m.imageAttachToken++
	token := m.imageAttachToken
	displayName := llm.SanitizeImageName(name)
	if displayName == "" {
		displayName = "clipboard.png"
	}
	if m.imageStore == nil {
		return func() tea.Msg {
			return ImageAttachmentResultMsg{Token: token, Preflight: true, Name: displayName, Err: errImageStoreUnavailable}
		}
	}
	if len(m.pendingImages) >= maxPendingImages {
		return func() tea.Msg {
			return ImageAttachmentResultMsg{Token: token, Preflight: true, Name: displayName, Err: errPendingImageLimitReached}
		}
	}
	if m.imageAttachCancel != nil {
		m.imageAttachCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	m.imageAttachCancel = cancel
	m.imageAttachRunning = true
	m.imageAttachFallback = ""
	m.input.Blur()
	store := m.imageStore
	payload := append([]byte(nil), data...)
	return tea.Batch(m.startActivityCmd(), func() tea.Msg {
		ref, err := store.AdmitBytes(ctx, displayName, payload)
		if err != nil {
			return ImageAttachmentResultMsg{Token: token, Name: displayName, Err: err}
		}
		image := llm.ImageData{
			SHA256: ref.Digest, Name: ref.Name, MediaType: ref.MIMEType,
			Size: ref.SizeBytes, Width: ref.Width, Height: ref.Height,
		}
		if err := image.ValidateReference(); err != nil {
			return ImageAttachmentResultMsg{Token: token, Name: ref.Name, Err: err}
		}
		return ImageAttachmentResultMsg{Token: token, Name: ref.Name, Ref: ref, Image: image}
	})
}

func (m *Model) handleImageAttachmentResult(message ImageAttachmentResultMsg) tea.Cmd {
	if message.Token == 0 || message.Token != m.imageAttachToken || (!message.Preflight && !m.imageAttachRunning) {
		return nil
	}
	if !message.Preflight {
		if m.imageAttachCancel != nil {
			m.imageAttachCancel()
		}
		m.imageAttachCancel = nil
		m.imageAttachRunning = false
		m.imageAttachFallback = ""
	}
	if m.shuttingDown {
		return nil
	}
	m.input.Focus()

	if message.Err != nil {
		m.entries = append(m.entries, ChatEntry{Kind: "error", Content: imageAttachmentErrorReceipt(message.Name, message.Err)})
		m.invalidateEntryCache()
		m.viewport.SetContent(m.renderEntries())
		m.gotoBottomIfFollowing()
		m.recalcViewportHeight()
		if message.Fallback != "" && m.composerEditable() {
			return m.insertPasteWithReview(message.Fallback)
		}
		return nil
	}
	for _, existing := range m.pendingImages {
		if existing.Ref.Digest == message.Ref.Digest {
			m.recalcViewportHeight()
			return nil
		}
	}
	refs := m.pendingImageRefs()
	refs = append(refs, message.Ref)
	if err := validateImageConversationBudget(m.agent.Messages(), refs); err != nil {
		m.entries = append(m.entries, ChatEntry{Kind: "error", Content: "Attach image: " + err.Error()})
		m.invalidateEntryCache()
		m.viewport.SetContent(m.renderEntries())
		m.gotoBottomIfFollowing()
		m.recalcViewportHeight()
		return nil
	}
	m.pendingImages = append(m.pendingImages, pendingImageAttachment{Ref: message.Ref, Image: message.Image})
	m.recalcViewportHeight()
	return nil
}

func imageAttachmentErrorReceipt(name string, err error) string {
	name = llm.SanitizeImageName(name)
	prefix := "Attach image"
	if name != "" {
		prefix += " " + sanitizeTerminalSingleLine(name)
	}
	var reason string
	switch {
	case errors.Is(err, errImageAttachmentBusy):
		reason = "another image is still being validated"
	case errors.Is(err, errImageStoreUnavailable):
		reason = "private image storage is unavailable"
	case errors.Is(err, errPendingImageLimitReached):
		reason = fmt.Sprintf("the pending prompt already has %d images; send it or run /image clear", maxPendingImages)
	case errors.Is(err, os.ErrNotExist):
		reason = "file was not found"
	case errors.Is(err, os.ErrPermission):
		reason = "file could not be read"
	case errors.Is(err, safeio.ErrTooLarge):
		reason = "file exceeds the 20 MiB limit"
	case errors.Is(err, safeio.ErrNotRegular):
		reason = "path is not a regular file"
	case errors.Is(err, safeio.ErrReadBusy):
		reason = "image reader is busy; try again"
	case errors.Is(err, safeio.ErrReadTimeout), errors.Is(err, context.DeadlineExceeded):
		reason = "validation timed out; try again"
	case errors.Is(err, context.Canceled):
		reason = "attachment was cancelled"
	case errors.Is(err, imageasset.ErrUnsupportedFormat):
		reason = "file is not a supported PNG, JPEG, or GIF"
	case errors.Is(err, imageasset.ErrInvalidDimensions):
		reason = "image dimensions exceed the supported limits"
	case errors.Is(err, imageasset.ErrIntegrity):
		reason = "private copy failed integrity verification"
	default:
		reason = "file could not be validated"
	}
	return prefix + ": " + reason
}

func (m *Model) insertPasteWithReview(content string) tea.Cmd {
	draft := m.input.Value()
	cursor := pasteCursorAt(draft, m.input.Line(), m.input.Column())
	assessment := assessPaste(content, cursor, m.input.Length(), m.input.LineCount(), m.input.CharLimit)
	if !assessment.PlainFits || assessment.NeedsReview {
		m.pendingPaste = assessment
		m.recalcViewportHeight()
		return nil
	}
	m.clearCompletionSuppression()
	m.input.InsertString(assessment.Content)
	m.syncInputHeight()
	return m.reflowInputViewport()
}

func (m *Model) clearPendingImages() int {
	count := len(m.pendingImages)
	for index := range m.pendingImages {
		m.pendingImages[index].Image = llm.ImageData{}
	}
	m.pendingImages = nil
	m.recalcViewportHeight()
	return count
}

func (m *Model) forgetHistoricalImages() tea.Cmd {
	if m.agent == nil {
		return nil
	}
	beforeMessages := m.agent.Messages()
	beforeEntries := append([]ChatEntry(nil), m.entries...)
	messages := cloneMessagesWithoutImageData(beforeMessages)
	removed := 0
	for index := range messages {
		removed += len(messages[index].Images)
		messages[index].Images = nil
	}
	visibleRemoved := 0
	for index := range m.entries {
		visibleRemoved += len(m.entries[index].Attachments)
	}
	if removed == 0 && visibleRemoved == 0 {
		m.entries = append(m.entries, ChatEntry{Kind: "system", Content: "No historical image references are present. Pending prompt images were left unchanged."})
		m.invalidateEntryCache()
		m.viewport.SetContent(m.renderEntries())
		m.resumeFollow()
		return nil
	}

	m.agent.ReplaceMessagesWithinSession(messages)
	for index := range m.entries {
		m.entries[index].Attachments = nil
	}
	reported := removed
	if reported == 0 {
		reported = visibleRemoved
	}
	m.entries = append(m.entries, ChatEntry{Kind: "system", Content: fmt.Sprintf(
		"Forgot %d historical image reference%s. Pending prompt images and private cached objects were left unchanged.",
		reported, pluralSuffix(reported),
	)})
	if m.sessionID > 0 && m.sessionStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := m.persistSessionState(ctx)
		cancel()
		if err != nil {
			m.agent.ReplaceMessagesWithinSession(beforeMessages)
			m.entries = beforeEntries
			m.entries = append(m.entries, ChatEntry{Kind: "error", Content: "Forget image history: saved session was not changed: " + sanitizeTerminalSingleLine(err.Error())})
		}
	}
	m.invalidateEntryCache()
	m.viewport.SetContent(m.renderEntries())
	m.resumeFollow()
	m.recalcViewportHeight()
	return nil
}

func cloneMessagesWithoutImageData(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return nil
	}
	cloned := append([]llm.Message(nil), messages...)
	for index := range cloned {
		cloned[index].Images = append([]llm.ImageData(nil), messages[index].Images...)
		for imageIndex := range cloned[index].Images {
			cloned[index].Images[imageIndex].Data = nil
		}
	}
	return cloned
}

func (m *Model) pendingImageRefs() []imageasset.Ref {
	refs := make([]imageasset.Ref, len(m.pendingImages))
	for index := range m.pendingImages {
		refs[index] = m.pendingImages[index].Ref
	}
	return refs
}

func clonePendingImages(images []pendingImageAttachment) []pendingImageAttachment {
	if len(images) == 0 {
		return nil
	}
	result := make([]pendingImageAttachment, len(images))
	for index, attachment := range images {
		result[index] = attachment
		result[index].Image.Data = append([]byte(nil), attachment.Image.Data...)
	}
	return result
}

func (m *Model) restoreTurnImages() {
	if len(m.turnImages) == 0 {
		return
	}
	// A failed pre-dispatch turn returns attachments ahead of anything the user
	// may already have prepared for the next prompt, while deduplicating by the
	// durable digest.
	combined := clonePendingImages(m.turnImages)
	seen := make(map[string]struct{}, len(combined))
	for _, attachment := range combined {
		seen[attachment.Ref.Digest] = struct{}{}
	}
	for _, attachment := range m.pendingImages {
		if _, duplicate := seen[attachment.Ref.Digest]; duplicate {
			continue
		}
		combined = append(combined, attachment)
	}
	m.pendingImages = combined
	m.turnImages = nil
}

func (m *Model) renderImageAttachmentSummary(refs []imageasset.Ref, width int) string {
	if len(refs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		label := fmt.Sprintf("%s · %dx%d · %s", ref.Name, ref.Width, ref.Height, ref.Handle())
		parts = append(parts, sanitizeTerminalSingleLine(label))
	}
	return m.styles.StatusText.Render(wrapText("  image · "+strings.Join(parts, "  |  "), max(1, width)))
}

func (m *Model) renderPendingImagesStatus(width int) string {
	if len(m.pendingImages) == 0 {
		return ""
	}
	names := make([]string, 0, len(m.pendingImages))
	for _, attachment := range m.pendingImages {
		names = append(names, sanitizeTerminalSingleLine(attachment.Ref.Name))
	}
	sort.Strings(names)
	detail := fmt.Sprintf("%d/%d · %s", len(names), maxPendingImages, strings.Join(names, ", "))
	return m.renderDecisionPrompt(
		"Images ready", truncateDisplay(detail, max(1, width-18)),
		keyHint{Key: "/image clear", Action: "remove"},
	)
}

func (m *Model) renderPlainImageList() string {
	if len(m.pendingImages) == 0 {
		return "No images are attached to the pending prompt."
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "%d pending image attachment%s:\n", len(m.pendingImages), pluralSuffix(len(m.pendingImages)))
	for _, attachment := range m.pendingImages {
		name := sanitizeTerminalSingleLine(attachment.Ref.Name)
		fmt.Fprintf(&builder, "- %s · %dx%d · %d bytes · %s\n",
			name, attachment.Ref.Width, attachment.Ref.Height,
			attachment.Ref.SizeBytes, attachment.Ref.Handle())
	}
	return strings.TrimRight(builder.String(), "\n")
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func imageOnlySessionTitle(images []pendingImageAttachment) string {
	if len(images) == 0 {
		return "Image review"
	}
	prefix := "Image · "
	if len(images) > 1 {
		prefix = "Images · "
	}
	title := prefix + images[0].Ref.Name
	if len(images) > 1 {
		title += fmt.Sprintf(" +%d", len(images)-1)
	}
	return sanitizeTerminalSingleLine(title)
}

func pastedImagePath(content string) (string, bool) {
	content = strings.TrimSpace(canonicalPasteContent(content))
	if content == "" || strings.Contains(content, "\n") {
		return "", false
	}
	if strings.HasPrefix(strings.ToLower(content), "file://") {
		parsed, err := url.Parse(content)
		if err != nil || parsed.Scheme != "file" || parsed.Host != "" {
			return "", false
		}
		content, err = url.PathUnescape(parsed.Path)
		if err != nil {
			return "", false
		}
	} else {
		fields, err := splitQuotedFields(content)
		if err != nil || len(fields) != 1 {
			return "", false
		}
		content = fields[0]
	}
	switch strings.ToLower(filepath.Ext(content)) {
	case ".png", ".jpg", ".jpeg", ".gif":
		return content, true
	default:
		return "", false
	}
}

func attachmentRefs(images []pendingImageAttachment) []imageasset.Ref {
	refs := make([]imageasset.Ref, len(images))
	for index := range images {
		refs[index] = images[index].Ref
	}
	return refs
}

func attachmentData(images []pendingImageAttachment) []llm.ImageData {
	data := make([]llm.ImageData, len(images))
	for index := range images {
		data[index] = images[index].Image
		data[index].Data = append([]byte(nil), images[index].Image.Data...)
	}
	return data
}

func imageRefsFromMessages(images []llm.ImageData) []imageasset.Ref {
	refs := make([]imageasset.Ref, 0, len(images))
	for _, image := range images {
		if image.ValidateReference() != nil {
			continue
		}
		refs = append(refs, imageasset.Ref{
			Digest: image.SHA256, MIMEType: image.MediaType, Name: image.Name,
			SizeBytes: image.Size, Width: image.Width, Height: image.Height,
		})
	}
	return refs
}

type imageConversationUsage struct {
	count  int
	bytes  int64
	pixels uint64
}

func (usage *imageConversationUsage) add(ref imageasset.Ref) error {
	if err := ref.Validate(); err != nil {
		return fmt.Errorf("invalid image reference: %w", err)
	}
	usage.count++
	usage.bytes += ref.SizeBytes
	usage.pixels += uint64(ref.Width) * uint64(ref.Height)
	if usage.count > maxConversationImages {
		return fmt.Errorf("the active conversation already carries %d images (limit %d); run /image forget-history", usage.count, maxConversationImages)
	}
	if usage.bytes > maxConversationImageBytes {
		return fmt.Errorf("active image context exceeds the %d MiB aggregate limit; run /image forget-history", maxConversationImageBytes>>20)
	}
	if usage.pixels > maxConversationImagePixels {
		return fmt.Errorf("active image context exceeds the %d-megapixel aggregate limit; run /image forget-history", maxConversationImagePixels/1_000_000)
	}
	return nil
}

func validateImageConversationBudget(messages []llm.Message, pending []imageasset.Ref) error {
	var usage imageConversationUsage
	for _, message := range messages {
		for _, ref := range imageRefsFromMessages(message.Images) {
			if err := usage.add(ref); err != nil {
				return err
			}
		}
	}
	for _, ref := range pending {
		if err := usage.add(ref); err != nil {
			return err
		}
	}
	return nil
}

func messagesRequireVision(messages []llm.Message) bool {
	for _, message := range messages {
		if len(message.Images) > 0 {
			return true
		}
	}
	return false
}

func (m *Model) currentModelSupportsVision() bool {
	descriptor, ok := m.ollamaModelDescriptor(m.model)
	if !ok || !hasOllamaCapability(descriptor.Capabilities, "vision") {
		return false
	}
	return m.modelPinned || (descriptor.Source == OllamaModelLocal && descriptor.AutoRoutable)
}

func (m *Model) ensureVisionModel() error {
	if m.currentModelSupportsVision() {
		return nil
	}
	if m.modelPinned {
		return fmt.Errorf("model %q is pinned but does not advertise vision; choose a vision model with Ctrl+P", sanitizeTerminalSingleLine(m.model))
	}
	candidates := append([]OllamaModelDescriptor(nil), m.ollamaModels...)
	sort.SliceStable(candidates, func(i, j int) bool {
		iLocal := candidates[i].Source == OllamaModelLocal
		jLocal := candidates[j].Source == OllamaModelLocal
		if iLocal != jLocal {
			return iLocal
		}
		if candidates[i].AutoRoutable != candidates[j].AutoRoutable {
			return candidates[i].AutoRoutable
		}
		return candidates[i].Name < candidates[j].Name
	})
	for _, candidate := range candidates {
		if candidate.Source != OllamaModelLocal || !candidate.Selectable || !candidate.Fit || !candidate.AutoRoutable || candidate.RequiresConsent ||
			!hasOllamaCapability(candidate.Capabilities, "vision") {
			continue
		}
		if m.modelManager != nil {
			m.prepareModelSwitch()
			if err := m.modelManager.SetCurrentModel(candidate.Name); err != nil {
				continue
			}
		}
		m.setCurrentModelProjection(candidate.Name)
		for index := range m.ollamaModels {
			m.ollamaModels[index].Current = config.CanonicalModelName(m.ollamaModels[index].Name) == config.CanonicalModelName(candidate.Name)
		}
		return nil
	}
	return fmt.Errorf("no admitted Ollama model advertises vision; open Ctrl+P and install or select a vision-capable model")
}

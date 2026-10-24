package types

// PanelTypeTicket marks an interactive panel that opens support tickets.
const PanelTypeTicket = "ticket"

// InteractivePanels is the root of the `interactive_panels` config section.
type InteractivePanels struct {
	Panels  map[string]*InteractivePanel `json:"panels"`
	Tickets TicketState                  `json:"tickets"`
}

// InteractivePanel is one guild-authored panel: the message members interact
// with, plus the behaviour behind its buttons.
type InteractivePanel struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Name       string             `json:"name"`
	ChannelID  string             `json:"channel_id"`
	MessageID  string             `json:"message_id"`
	Appearance PanelAppearance    `json:"appearance"`
	Ticket     *TicketPanelConfig `json:"ticket,omitempty"`
	CreatedBy  string             `json:"created_by"`
	CreatedAt  int64              `json:"created_at"`
	UpdatedAt  int64              `json:"updated_at"`
}

// PanelAppearance controls how the panel message looks. Text fields accept
// Discord markdown.
type PanelAppearance struct {
	Format           string `json:"format,omitempty"` // message, embed or component (Components V2)
	Title            string `json:"title"`
	Description      string `json:"description"`
	Footer           string `json:"footer"`
	Color            int    `json:"color"`
	NoColor          bool   `json:"no_color,omitempty"` // no side colour: the panel is one plain block, without the bar on its left
	BannerURL        string `json:"banner_url"`
	HideCategoryList bool   `json:"hide_category_list"`
	// Layout is the order of everything in the panel message. Empty: the standard layout (see StandardPanelLayout).
	Layout []PanelBlock `json:"layout,omitempty"`
}

// PanelBlock is one part of a panel message. The built-in types (banner, title, description, categories, footer,
// buttons) show what the panel's own fields hold; the others carry their own content.
type PanelBlock struct {
	ID   string `json:"id"`
	Type string `json:"type"` // banner, title, description, categories, footer, buttons, text, separator, gallery, section, links

	Content             string      `json:"content,omitempty"`               // text
	Divider             *bool       `json:"divider,omitempty"`               // separator: a visible line (default true)
	Large               bool        `json:"large,omitempty"`                 // separator: large spacing
	Items               []PanelItem `json:"items,omitempty"`                 // gallery
	Text                string      `json:"text,omitempty"`                  // section
	ThumbnailURL        string      `json:"thumbnail_url,omitempty"`         // section
	AccessoryType       string      `json:"accessory_type,omitempty"`        // section: thumbnail or ticket_button
	AccessoryCategoryID string      `json:"accessory_category_id,omitempty"` // section ticket button
	Links               []PanelLink `json:"links,omitempty"`                 // links
}

// PanelItem is one image of a gallery block.
type PanelItem struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// PanelLink is one link button of a links block.
type PanelLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

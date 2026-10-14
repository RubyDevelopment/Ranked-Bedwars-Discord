package types

// Asset is a picture uploaded in the builder, a data URL; layers use it as "asset:<id>".
type Asset struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

// Background is the canvas fill: a color or a two-color gradient, then an optional image, then a tint.
type Background struct {
	Color          string  `json:"color"`
	Color2         string  `json:"color2"`
	Angle          float64 `json:"angle"`
	Image          string  `json:"image"`
	Fit            string  `json:"fit"`
	Blur           float64 `json:"blur"`
	Overlay        string  `json:"overlay"`
	OverlayOpacity float64 `json:"overlayOpacity"`
}

// Layer is one drawn element. Every kind uses the fields it needs and ignores the rest.
type Layer struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"` // text, image, shape
	X        float64  `json:"x"`
	Y        float64  `json:"y"`
	W        float64  `json:"w"`
	H        float64  `json:"h"`
	Rotation float64  `json:"rotation"`
	Opacity  *float64 `json:"opacity"` // nil = fully visible
	Hidden   bool     `json:"hidden"`
	Shadow   Shadow   `json:"shadow"`
	Rules    []Rule   `json:"rules"`

	Text          string  `json:"text"`
	Font          string  `json:"font"` // sans, medium, mono, smallcaps
	Size          float64 `json:"size"`
	Bold          bool    `json:"bold"`
	Italic        bool    `json:"italic"`
	Align         string  `json:"align"`  // left, center, right
	VAlign        string  `json:"valign"` // top, middle, bottom
	LetterSpacing float64 `json:"letterSpacing"`

	Color  string  `json:"color"`  // text color / shape fill
	Color2 string  `json:"color2"` // shape gradient end
	Angle  float64 `json:"angle"`

	Src  string  `json:"src"`
	Blur float64 `json:"blur"`
	Fit  string  `json:"fit"` // cover, contain

	Radius      float64 `json:"radius"`
	Shape       string  `json:"shape"` // rect, ellipse
	Stroke      string  `json:"stroke"`
	StrokeWidth float64 `json:"strokeWidth"`
}

// Rule restyles a layer when the data matches (`var op value`; numbers compare as numbers). Matching rules
// apply in order. A Set field that is nil leaves the layer's own value alone.
type Rule struct {
	Var   string  `json:"var"`
	Op    string  `json:"op"` // eq, ne, contains, gt, gte, lt, lte
	Value string  `json:"value"`
	Set   RuleSet `json:"set"`
}

// RuleSet is what a matching rule changes.
type RuleSet struct {
	Color   *string  `json:"color"`
	Color2  *string  `json:"color2"`
	Stroke  *string  `json:"stroke"`
	Opacity *float64 `json:"opacity"`
	Hidden  *bool    `json:"hidden"`
}

// Shadow is a drop shadow behind a layer.
type Shadow struct {
	On    bool    `json:"on"`
	Color string  `json:"color"`
	Blur  float64 `json:"blur"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

// Viewer is who a banner is being chosen for.
type Viewer struct {
	UserID  string
	RoleIDs []string
	Booster bool
	// PerkIDs are the perks the player has. The bot has no perk system yet, so callers pass none and
	// banners limited to a perk stay closed to everyone until it exists.
	PerkIDs []string
}

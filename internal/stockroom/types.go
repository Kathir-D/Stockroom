package stockroom

import "time"

// Row types for every table and view in the base schema
// (supabase/migrations/20260826173006_init_schema.sql). Columns added by the
// v1 migration (CLAUDE.md §6.2) are marked "v1" and will be nil/zero until
// that migration is applied. Tables that v1 does not use (locations, tags,
// bookings, saved_filters) still get structs so backup/export can read them.

type UserRole string

const (
	RoleOwner             UserRole = "owner"
	RoleExecutiveProducer UserRole = "executive_producer"
	RoleProducer          UserRole = "producer"
	RoleMember            UserRole = "member"
)

type AssetStatus string

const (
	StatusAvailable   AssetStatus = "available"
	StatusCheckedOut  AssetStatus = "checked_out"
	StatusUnavailable AssetStatus = "unavailable" // v1; catch-all for broken/missing/retired
	StatusReserved    AssetStatus = "reserved"    // unused in v1
	StatusMaintenance AssetStatus = "maintenance" // unused in v1
	StatusRetired     AssetStatus = "retired"     // unused in v1
	StatusLost        AssetStatus = "lost"        // unused in v1
)

type BookingStatus string

const (
	BookingReserved  BookingStatus = "reserved"
	BookingActive    BookingStatus = "active"
	BookingReturned  BookingStatus = "returned"
	BookingOverdue   BookingStatus = "overdue"
	BookingCancelled BookingStatus = "cancelled"
)

type LocationType string

const (
	LocationBuilding LocationType = "building"
	LocationFloor    LocationType = "floor"
	LocationRoom     LocationType = "room"
	LocationShelf    LocationType = "shelf"
	LocationOther    LocationType = "other"
)

type Profile struct {
	ID            string    `json:"id"`
	Email         *string   `json:"email"`
	PasswordHash  *string   `json:"-"`
	FullName      *string   `json:"full_name"`
	Role          UserRole  `json:"role"`           // unused in v1; IsAdmin is the permission flag
	StudentNumber *string   `json:"student_number"` // v1
	FirstName     *string   `json:"first_name"`     // v1
	LastName      *string   `json:"last_name"`      // v1
	PhotoPath     *string   `json:"photo_path"`     // v1
	IsAdmin       bool      `json:"is_admin"`       // v1
	CreatedAt     time.Time `json:"created_at"`
}

type Location struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Type      LocationType `json:"type"`
	ParentID  *string      `json:"parent_id"`
	GPSLat    *float64     `json:"gps_lat"`
	GPSLng    *float64     `json:"gps_lng"`
	CreatedAt time.Time    `json:"created_at"`
}

type Category struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ParentID  *string   `json:"parent_id"`
	CreatedAt time.Time `json:"created_at"`
}

type Tag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Asset struct {
	ID                 string         `json:"id"`
	AssetTag           string         `json:"asset_tag"`
	Name               string         `json:"name"`
	Description        *string        `json:"description"`
	CategoryID         *string        `json:"category_id"`
	LocationID         *string        `json:"location_id"`
	Status             AssetStatus    `json:"status"`
	Condition          *string        `json:"condition"`
	SerialNumber       *string        `json:"serial_number"` // scan key (unique in v1)
	PurchaseDate       *time.Time     `json:"purchase_date"`
	PurchasePrice      *float64       `json:"purchase_price"`
	WarrantyExpiration *time.Time     `json:"warranty_expiration"`
	CustomFields       map[string]any `json:"custom_fields"`
	PhotoPath          *string        `json:"photo_path"` // v1
	CreatedBy          *string        `json:"created_by"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

type AssetTag struct {
	AssetID string `json:"asset_id"`
	TagID   string `json:"tag_id"`
}

type Kit struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type KitItem struct {
	KitID   string `json:"kit_id"`
	AssetID string `json:"asset_id"`
}

type Booking struct {
	ID         string        `json:"id"`
	AssetID    *string       `json:"asset_id"`
	KitID      *string       `json:"kit_id"`
	ReservedBy string        `json:"reserved_by"`
	StartDate  time.Time     `json:"start_date"`
	EndDate    time.Time     `json:"end_date"`
	Status     BookingStatus `json:"status"`
	Notes      *string       `json:"notes"`
	CreatedAt  time.Time     `json:"created_at"`
}

type CustodyEvent struct {
	ID           string     `json:"id"`
	AssetID      string     `json:"asset_id"`
	BookingID    *string    `json:"booking_id"`
	CustodianID  string     `json:"custodian_id"`
	CheckedOutBy string     `json:"checked_out_by"`
	CheckedOutAt time.Time  `json:"checked_out_at"`
	DueAt        *time.Time `json:"due_at"`
	CheckedInAt  *time.Time `json:"checked_in_at"`
	CheckedInBy  *string    `json:"checked_in_by"`
	ConditionOut *string    `json:"condition_out"`
	ConditionIn  *string    `json:"condition_in"`
	Notes        *string    `json:"notes"`
}

// ActiveCustody mirrors the active_custody view (custody_events joined with
// the asset's name and tag, where checked_in_at is null). overdue_custody has
// the same shape, filtered to due_at < now().
type ActiveCustody struct {
	CustodyEvent
	AssetName string `json:"asset_name"`
	AssetTag  string `json:"asset_tag"`
}

type ActivityLog struct {
	ID        string         `json:"id"`
	AssetID   *string        `json:"asset_id"`
	ActorID   *string        `json:"actor_id"`
	Action    string         `json:"action"`
	Details   map[string]any `json:"details"`
	CreatedAt time.Time      `json:"created_at"`
}

type SavedFilter struct {
	ID         string         `json:"id"`
	UserID     string         `json:"user_id"`
	Name       string         `json:"name"`
	FilterJSON map[string]any `json:"filter_json"`
	CreatedAt  time.Time      `json:"created_at"`
}

package protocol

import "encoding/json"

const RoleBody = "minecraft-body"
const BodyVersion = 1
const (
	TypeBodyState         = "body.state"
	TypeBodyEvent         = "body.event"
	TypeBodyResult        = "body.result"
	TypeBodyCall          = "body.call"
	TypeBodyInspect       = "body.inspect"
	TypeBodyInspectResult = "body.inspect_result"
	TypeBodyControl       = "body.control"
	TypeBodyControlAck    = "body.control_ack"
	TypeBodyPolicy        = "body.policy"
	TypeBodyPolicyAck     = "body.policy_ack"
	TypeBodyLease         = "body.lease"
)

type BodyArgs struct {
	Movement   string   `json:"movement,omitempty"`
	X          *float64 `json:"x,omitempty"`
	Y          *float64 `json:"y,omitempty"`
	Z          *float64 `json:"z,omitempty"`
	Player     string   `json:"player,omitempty"`
	EntityID   *int     `json:"entityId,omitempty"`
	Range      int      `json:"range,omitempty"`
	TimeoutSec int      `json:"timeoutSec,omitempty"`
	Blocks     string   `json:"blocks,omitempty"`
	Radius     int      `json:"radius,omitempty"`
	Count      int      `json:"count,omitempty"`
	Filter     string   `json:"filter,omitempty"`
	Face       string   `json:"face,omitempty"`
	Item       string   `json:"item,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	HoldMS     int      `json:"holdMs,omitempty"`
	Text       string   `json:"text,omitempty"`
	Command    string   `json:"command,omitempty"`
}
type BodyState struct {
	ConnectionID    string     `json:"connectionId"`
	ControlEpoch    uint64     `json:"controlEpoch"`
	PolicyRevision  uint64     `json:"policyRevision"`
	Seq             uint64     `json:"seq"`
	Connected       bool       `json:"connected"`
	Paused          bool       `json:"paused"`
	PauseReason     string     `json:"pauseReason,omitempty"`
	Name            string     `json:"name"`
	WorldID         string     `json:"worldId"`
	Dimension       string     `json:"dimension"`
	Pos             [3]float64 `json:"pos"`
	Yaw             float64    `json:"yaw"`
	Pitch           float64    `json:"pitch"`
	Health          float64    `json:"health"`
	Food            int        `json:"food"`
	GameMode        string     `json:"gameMode"`
	HeldItem        string     `json:"heldItem"`
	Task            *BodyTask  `json:"task"`
	UpdatedAt       int64      `json:"updatedAt"`
	OnlineSince     *int64     `json:"onlineSince"`
	ClientMcVersion string     `json:"clientMcVersion"`
	TargetMcVersion string     `json:"targetMcVersion"`
	ViaProxy        bool       `json:"viaProxy"`
}
type BodyTask struct {
	ID        string `json:"id"`
	Tool      string `json:"tool"`
	StartedAt int64  `json:"startedAt"`
}
type BodyEvent struct {
	ConnectionID string          `json:"connectionId"`
	Type         string          `json:"type"`
	At           int64           `json:"at"`
	Data         json.RawMessage `json:"data,omitempty"`
}

// Prepared actions are immutable and stored by the body; execute accepts only their opaque IDs.
type BodySpec struct {
	ID             string      `json:"id"`
	Tool           string      `json:"tool"`
	Args           BodyArgs    `json:"args"`
	ConnectionID   string      `json:"connectionId"`
	ControlEpoch   uint64      `json:"controlEpoch"`
	PolicyRevision uint64      `json:"policyRevision"`
	ExpiresAt      int64       `json:"expiresAt"`
	WorldID        string      `json:"worldId"`
	Dimension      string      `json:"dimension"`
	Pos            [3]float64  `json:"pos"`
	Target         *[3]float64 `json:"target,omitempty"`
	Block          string      `json:"block"`
	BlockState     int         `json:"blockState"`
	Support        string      `json:"support"`
	SupportState   int         `json:"supportState"`
	MainHand       string      `json:"mainHand"`
	OffHand        string      `json:"offHand"`
	Slot           int         `json:"slot"`
	Count          int         `json:"count"`
	Hazards        []string    `json:"hazards"`
	Protected      bool        `json:"protected,omitempty"`
}
type BodyInspect struct {
	InspectID      string   `json:"inspectId"`
	ConnectionID   string   `json:"connectionId"`
	ControlEpoch   uint64   `json:"controlEpoch"`
	PolicyRevision uint64   `json:"policyRevision"`
	Tool           string   `json:"tool"`
	Args           BodyArgs `json:"args"`
	ExpiresAt      int64    `json:"expiresAt"`
}
type BodyInspectResult struct {
	Data         json.RawMessage `json:"data,omitempty"`
	InspectID    string          `json:"inspectId"`
	ConnectionID string          `json:"connectionId"`
	OK           bool            `json:"ok"`
	Code         string          `json:"code"`
	Error        string          `json:"error"`
	Spec         *BodySpec       `json:"spec"`
}
type BodyGrant struct {
	ApprovalID   string `json:"approvalId"`
	SpecID       string `json:"specId"`
	ExpiresAt    int64  `json:"expiresAt"`
	ControlEpoch uint64 `json:"controlEpoch"`
}
type BodyCall struct {
	RunID          string     `json:"runId,omitempty"`
	CallID         string     `json:"callId"`
	ConnectionID   string     `json:"connectionId"`
	ControlEpoch   uint64     `json:"controlEpoch"`
	PolicyRevision uint64     `json:"policyRevision"`
	Tool           string     `json:"tool"`
	Args           BodyArgs   `json:"args"`
	SpecID         string     `json:"specId,omitempty"`
	Grant          *BodyGrant `json:"approvalGrant,omitempty"`
	TimeoutMS      int        `json:"timeoutMs"`
}
type BodyResult struct {
	CallID       string          `json:"callId"`
	ConnectionID string          `json:"connectionId"`
	ControlEpoch uint64          `json:"controlEpoch"`
	OK           bool            `json:"ok"`
	Outcome      string          `json:"outcome"`
	Code         string          `json:"code"`
	Error        string          `json:"error"`
	Summary      string          `json:"summary"`
	Data         json.RawMessage `json:"data,omitempty"`
	State        *BodyState      `json:"state,omitempty"`
	ElapsedMS    int64           `json:"elapsedMs"`
}
type BodyControl struct {
	CallID       string `json:"callId,omitempty"`
	ID           string `json:"id"`
	ConnectionID string `json:"connectionId"`
	ControlEpoch uint64 `json:"controlEpoch"`
	Action       string `json:"action"`
}
type BodyAck struct {
	ID             string `json:"id"`
	ConnectionID   string `json:"connectionId"`
	ControlEpoch   uint64 `json:"controlEpoch"`
	PolicyRevision uint64 `json:"policyRevision"`
	Paused         bool   `json:"paused"`
}

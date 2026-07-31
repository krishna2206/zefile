package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/krishna2206/zefile/internal/acl"
)

type groupResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	MemberCount int64  `json:"member_count"`
}

type groupListResponse struct {
	Groups []groupResponse `json:"groups"`
}

// handleListGroups returns every group with its member count. Admin only.
func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	groups, err := s.acl.ListGroups(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]groupResponse, 0, len(groups))
	for _, g := range groups {
		out = append(out, groupResponse{ID: g.ID, Name: g.Name, MemberCount: g.MemberCount})
	}
	writeJSON(w, r, http.StatusOK, groupListResponse{Groups: out})
}

type createGroupRequest struct {
	Name string `json:"name"`
}

// handleCreateGroup makes a new group. Admin only.
func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var body createGroupRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeFieldProblem(w, r, map[string]string{"name": "Choose a name for the group."})
		return
	}

	group, err := s.acl.CreateGroup(r.Context(), name)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			writeProblem(w, r, http.StatusConflict, CodeConflict, "Name taken", "A group with that name already exists.")
			return
		}
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, groupResponse{ID: group.ID, Name: group.Name})
}

// handleDeleteGroup removes a group and the rules granted to it. Admin only.
func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	id, ok := groupIDParam(w, r)
	if !ok {
		return
	}
	if err := s.acl.DeleteGroup(r.Context(), id); err != nil {
		if errors.Is(err, acl.ErrGroupNotFound) {
			writeProblem(w, r, http.StatusNotFound, CodeNotFound, "No such group", "This group does not exist.")
			return
		}
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusNoContent, nil)
}

type groupMembersResponse struct {
	MemberIDs []int64 `json:"member_ids"`
}

// handleListGroupMembers returns the ids of a group's members. Admin only.
func (s *Server) handleListGroupMembers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	id, ok := groupIDParam(w, r)
	if !ok {
		return
	}
	ids, err := s.acl.GroupMemberIDs(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, groupMembersResponse{MemberIDs: ids})
}

// handleAddGroupMember adds a user to a group. Admin only.
func (s *Server) handleAddGroupMember(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	groupID, ok := groupIDParam(w, r)
	if !ok {
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest, "Bad id", "The user id must be a number.")
		return
	}
	if err := s.acl.AddGroupMember(r.Context(), groupID, userID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusNoContent, nil)
}

// handleRemoveGroupMember takes a user out of a group. Admin only.
func (s *Server) handleRemoveGroupMember(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	groupID, ok := groupIDParam(w, r)
	if !ok {
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest, "Bad id", "The user id must be a number.")
		return
	}
	if err := s.acl.RemoveGroupMember(r.Context(), groupID, userID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusNoContent, nil)
}

func groupIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, CodeBadRequest, "Bad id", "The group id must be a number.")
		return 0, false
	}
	return id, true
}

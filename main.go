// 9beads-acme - Acme interface for beads task tracking
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"9fans.net/go/acme"
	"9fans.net/go/plan9"
	"9fans.net/go/plan9/client"
)

var beadsFs *client.Fsys

func main() {
	var err error
	beadsFs, err = connectToBeads()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to 9beads: %v\n", err)
		os.Exit(1)
	}

	if err := openTasksWindow(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open tasks window: %v\n", err)
		os.Exit(1)
	}

	// Block forever
	select {}
}

func connectToBeads() (*client.Fsys, error) {
	ns := client.Namespace()
	if ns == "" {
		return nil, fmt.Errorf("no namespace")
	}
	return client.MountService("beads")
}

func isBeadsConnected() bool {
	return beadsFs != nil
}

func readBeadsFile(path string) ([]byte, error) {
	if !isBeadsConnected() {
		return nil, fmt.Errorf("not connected to 9beads")
	}
	fid, err := beadsFs.Open(path, plan9.OREAD)
	if err != nil {
		return nil, err
	}
	defer fid.Close()

	var buf []byte
	tmp := make([]byte, 8192)
	for {
		n, err := fid.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return buf, nil
}

func writeBeadsFile(path string, data []byte) error {
	if !isBeadsConnected() {
		return fmt.Errorf("not connected to 9beads")
	}
	fid, err := beadsFs.Open(path, plan9.OWRITE)
	if err != nil {
		return err
	}
	defer fid.Close()

	_, err = fid.Write(data)
	return err
}

type Bead struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Assignee    string   `json:"assignee"`
	Priority    int      `json:"priority"`
	Blockers    []string `json:"blockers,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	CloseReason string   `json:"close_reason,omitempty"`
}

func (b *Bead) HasLabel(label string) bool {
	for _, l := range b.Labels {
		if l == label {
			return true
		}
	}
	return false
}

type beadEdit struct {
	action string
	beadID string
	title  string
}

func parseIndex(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func shellEscape(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}

func parseBeadEdits(content string, beads []Bead) []beadEdit {
	var edits []beadEdit
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		switch {
		case strings.HasPrefix(line, "- "):
			rest := strings.TrimSpace(line[2:])
			if idx := parseIndex(rest); idx > 0 && idx <= len(beads) {
				edits = append(edits, beadEdit{action: "delete", beadID: beads[idx-1].ID})
			}
		case strings.HasPrefix(line, "c "):
			rest := strings.TrimSpace(line[2:])
			if idx := parseIndex(rest); idx > 0 && idx <= len(beads) {
				edits = append(edits, beadEdit{action: "claim", beadID: beads[idx-1].ID})
			}
		case strings.HasPrefix(line, "u "):
			rest := strings.TrimSpace(line[2:])
			if idx := parseIndex(rest); idx > 0 && idx <= len(beads) {
				edits = append(edits, beadEdit{action: "unclaim", beadID: beads[idx-1].ID})
			}
		case strings.HasPrefix(line, "o "):
			rest := strings.TrimSpace(line[2:])
			if idx := parseIndex(rest); idx > 0 && idx <= len(beads) {
				edits = append(edits, beadEdit{action: "open", beadID: beads[idx-1].ID})
			}
		case strings.HasPrefix(line, "d "):
			rest := strings.TrimSpace(line[2:])
			if idx := parseIndex(rest); idx > 0 && idx <= len(beads) {
				edits = append(edits, beadEdit{action: "defer", beadID: beads[idx-1].ID})
			}
		case strings.HasPrefix(line, "x "):
			rest := strings.TrimSpace(line[2:])
			if idx := parseIndex(rest); idx > 0 && idx <= len(beads) {
				edits = append(edits, beadEdit{action: "complete", beadID: beads[idx-1].ID})
			}
		case strings.HasPrefix(line, "f "):
			rest := strings.TrimSpace(line[2:])
			if idx := parseIndex(rest); idx > 0 && idx <= len(beads) {
				edits = append(edits, beadEdit{action: "fail", beadID: beads[idx-1].ID})
			}
		case strings.HasPrefix(line, "+ "):
			title := strings.TrimSpace(line[2:])
			if title != "" {
				edits = append(edits, beadEdit{action: "create", title: title})
			}
		}
	}
	return edits
}

func applyBeadEdits(w *acme.Win, edits []beadEdit, beads *[]Bead, filter string, mount string) {
	if len(edits) == 0 {
		return
	}
	var errs []string
	for _, edit := range edits {
		var err error
		switch edit.action {
		case "delete":
			err = deleteBead(edit.beadID, mount)
		case "claim":
			err = claimBeadAsUser(edit.beadID, mount)
		case "unclaim":
			err = unclaimBead(edit.beadID, mount)
		case "open":
			err = openBeadStatus(edit.beadID, mount)
		case "defer":
			err = deferBead(edit.beadID, mount)
		case "complete":
			err = completeBead(edit.beadID, mount)
		case "fail":
			err = failBead(edit.beadID, "user-closed", mount)
		case "create":
			err = openNewBeadWindowWithTitle(edit.title, mount)
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s %s: %v", edit.action, edit.beadID, err))
		}
	}
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "beadEdit error: %s\n", e)
	}
	refreshed, _ := listBeadsWithFilter(filter, mount)
	*beads = refreshed
	refreshTasksWindowWithBeads(w, *beads, mount, filter)
}

func openTasksWindow() error {
	w, err := acme.New()
	if err != nil {
		return err
	}
	w.Name("/Beads/Tasks")
	w.Write("tag", []byte("Get Put New Remove Init Mount Umount Select "))
	go handleTasksWindow(w)
	return nil
}

func handleTasksWindow(w *acme.Win) {
	defer w.CloseFiles()

	var beads []Bead
	filter := "all"
	windowMount := ""
	beads, _ = listBeadsWithFilter(filter, windowMount)
	refreshTasksWindowWithBeads(w, beads, windowMount, filter)

	for e := range w.EventChan() {
		switch e.C2 {
		case 'x', 'X':
			cmd := string(e.Text)
			arg := strings.TrimSpace(string(e.Arg))
			switch cmd {
			case "Get":
				beads, _ = listBeadsWithFilter(filter, windowMount)
				refreshTasksWindowWithBeads(w, beads, windowMount, filter)
			case "Put":
				body, _ := w.ReadAll("body")
				edits := parseBeadEdits(string(body), beads)
				applyBeadEdits(w, edits, &beads, filter, windowMount)
			case "New":
				openNewBeadWindow("", windowMount)
			case "Remove":
				if arg != "" {
					deleteBead(arg, windowMount)
					beads, _ = listBeadsWithFilter(filter, windowMount)
					refreshTasksWindowWithBeads(w, beads, windowMount, filter)
				}
			case "Init":
				prefix := arg
				if prefix == "" {
					prefix = "bd"
				}
				initBeads(prefix, windowMount)
				beads, _ = listBeadsWithFilter(filter, windowMount)
				refreshTasksWindowWithBeads(w, beads, windowMount, filter)
			case "Mount":
				if arg != "" {
					if name, err := mountProject(arg); err == nil {
						windowMount = name
						beads, _ = listBeadsWithFilter(filter, windowMount)
						refreshTasksWindowWithBeads(w, beads, windowMount, filter)
					}
				}
			case "Umount":
				if arg != "" {
					umountProject(arg)
					if windowMount == arg {
						windowMount = ""
					}
					beads, _ = listBeadsWithFilter(filter, windowMount)
					refreshTasksWindowWithBeads(w, beads, windowMount, filter)
				}
			case "Select":
				if arg != "" {
					windowMount = arg
					beads, _ = listBeadsWithFilter(filter, windowMount)
					refreshTasksWindowWithBeads(w, beads, windowMount, filter)
				} else {
					mounts := listMounts()
					if len(mounts) == 0 {
						w.Err("Select <mount> — no mounts available")
					} else {
						w.Err("Select <mount> — available: " + strings.Join(mounts, ", "))
					}
				}
			case "Deferred":
				filter = "deferred"
				beads, _ = listBeadsWithFilter(filter, windowMount)
				refreshTasksWindowWithBeads(w, beads, windowMount, filter)
			case "Ready":
				filter = "ready"
				beads, _ = listBeadsWithFilter(filter, windowMount)
				refreshTasksWindowWithBeads(w, beads, windowMount, filter)
			case "All":
				filter = "all"
				beads, _ = listBeadsWithFilter(filter, windowMount)
				refreshTasksWindowWithBeads(w, beads, windowMount, filter)
			default:
				w.WriteEvent(e)
			}
		case 'l', 'L':
			text := strings.TrimSpace(string(e.Text))
			if idx := parseIndex(text); idx > 0 && idx <= len(beads) {
				openViewBeadWindow(beads[idx-1].ID, windowMount)
			} else {
				w.WriteEvent(e)
			}
		default:
			w.WriteEvent(e)
		}
	}
}

func refreshTasksWindowWithBeads(w *acme.Win, beads []Bead, mount string, filter string) {
	if mount == "" {
		w.Name("/Beads/Tasks")
	} else {
		w.Name(fmt.Sprintf("/Beads/Tasks [%s]", mount))
	}
	var buf strings.Builder

	if mount == "" {
		buf.WriteString("No project mounted.\n\nUse Mount to mount a project directory.\n")
	} else {
		buf.WriteString("[Deferred] [Ready] [All]\n\n")
		buf.WriteString(fmt.Sprintf("%-4s %-12s %-12s %-4s %-8s %s\n", "#", "ID", "Status", "Blk", "Assignee", "Title"))
		buf.WriteString(fmt.Sprintf("%-4s %-12s %-12s %-4s %-8s %s\n", "----", "------------", "------------", "----", "--------", strings.Repeat("-", 50)))
		for i, b := range beads {
			assignee := b.Assignee
			if assignee == "" {
				assignee = "-"
			}
			blk := "-"
			if len(b.Blockers) > 0 {
				blk = fmt.Sprintf("%d", len(b.Blockers))
			}
			buf.WriteString(fmt.Sprintf("%-4d %-12s %-12s %-4s %-8s %s\n", i+1, b.ID, b.Status, blk, assignee, b.Title))
		}
	}

	w.Addr(",")
	w.Write("data", []byte(buf.String()))
	w.Ctl("clean")
	w.Addr("0")
	w.Ctl("dot=addr")
	w.Ctl("show")
}

func listBeadsWithFilter(filter string, mount string) ([]Bead, error) {
	if mount == "" && filter != "deferred" && filter != "ready" {
		return nil, nil
	}

	var endpoint string
	switch filter {
	case "deferred":
		if mount == "" {
			endpoint = "deferred"
		} else {
			endpoint = filepath.Join(mount, "deferred")
		}
	case "ready":
		if mount == "" {
			endpoint = "ready"
		} else {
			endpoint = filepath.Join(mount, "ready")
		}
	default:
		endpoint = filepath.Join(mount, "list")
	}

	data, err := readBeadsFile(endpoint)
	if err != nil {
		return nil, err
	}

	var beads []Bead
	json.Unmarshal(data, &beads)
	return beads, nil
}

func openNewBeadWindow(parentID string, mount string) error {
	w, err := acme.New()
	if err != nil {
		return err
	}
	w.Name("/Beads/Tasks/+new")
	w.Write("tag", []byte("Put "))
	w.Write("body", []byte("---\ntitle:\nblockers:\n---\n"))
	w.Ctl("clean")
	go handleNewBeadWindow(w, parentID, mount)
	return nil
}

func openNewBeadWindowWithTitle(title string, mount string) error {
	w, err := acme.New()
	if err != nil {
		return err
	}
	w.Name("/Beads/Tasks/+new")
	w.Write("tag", []byte("Put "))
	w.Write("body", []byte(fmt.Sprintf("---\ntitle: %s\nblockers:\n---\n", title)))
	w.Ctl("clean")
	go handleNewBeadWindow(w, "", mount)
	return nil
}

func handleNewBeadWindow(w *acme.Win, parentID string, mount string) {
	defer w.CloseFiles()
	for e := range w.EventChan() {
		if (e.C2 == 'x' || e.C2 == 'X') && string(e.Text) == "Put" {
			body, _ := w.ReadAll("body")
			createBeadFromMarkdown(string(body), parentID, mount)
			w.Ctl("delete")
			return
		}
		w.WriteEvent(e)
	}
}

func createBeadFromMarkdown(content, parentID string, mount string) error {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return fmt.Errorf("missing frontmatter")
	}
	parts := strings.SplitN(content[3:], "---", 2)
	frontmatter := strings.TrimSpace(parts[0])
	description := ""
	if len(parts) > 1 {
		description = strings.TrimSpace(parts[1])
	}

	var title, blockers string
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "title:") {
			title = strings.TrimSpace(strings.TrimPrefix(line, "title:"))
		} else if strings.HasPrefix(line, "blockers:") {
			blockers = strings.TrimSpace(strings.TrimPrefix(line, "blockers:"))
		}
	}

	if title == "" {
		return fmt.Errorf("title required")
	}

	cmd := fmt.Sprintf("new '%s' '%s'", shellEscape(title), shellEscape(description))
	if parentID != "" {
		cmd += " " + parentID
	}
	if blockers != "" {
		cmd += " blockers=" + blockers
	}
	return writeBeadsFile(filepath.Join(mount, "ctl"), []byte(cmd))
}

func openViewBeadWindow(beadID string, mount string) error {
	w, err := acme.New()
	if err != nil {
		return err
	}
	w.Name(fmt.Sprintf("/Beads/Tasks/%s", beadID))
	w.Write("tag", []byte("Get Put New Blocks Comments Comment "))
	go handleViewBeadWindow(w, beadID, mount)
	return nil
}

func handleViewBeadWindow(w *acme.Win, beadID string, mount string) {
	defer w.CloseFiles()
	bead, _ := getBead(beadID, mount)
	var origBlockers []string
	if bead != nil {
		origBlockers = bead.Blockers
	}
	refreshViewBeadWindow(w, beadID, mount)

	for e := range w.EventChan() {
		switch e.C2 {
		case 'x', 'X':
			cmd := string(e.Text)
			arg := strings.TrimSpace(string(e.Arg))
			switch cmd {
			case "Get":
				bead, _ = getBead(beadID, mount)
				if bead != nil {
					origBlockers = bead.Blockers
				}
				refreshViewBeadWindow(w, beadID, mount)
			case "Put":
				body, _ := w.ReadAll("body")
				updateBead(beadID, string(body), origBlockers, mount)
				bead, _ = getBead(beadID, mount)
				if bead != nil {
					origBlockers = bead.Blockers
				}
				w.Ctl("clean")
			case "New":
				openNewBeadWindow(beadID, mount)
			case "Blocks":
				if arg != "" {
					addBlocksDep(beadID, arg, mount)
				}
			case "Comments":
				openCommentsWindow(beadID, mount)
			case "Comment":
				openCommentWindow(beadID, mount)
			default:
				w.WriteEvent(e)
			}
		default:
			w.WriteEvent(e)
		}
	}
}

func refreshViewBeadWindow(w *acme.Win, beadID string, mount string) {
	bead, err := getBead(beadID, mount)
	if err != nil {
		w.Addr(",")
		w.Write("data", []byte(fmt.Sprintf("Error: %v\n", err)))
		w.Ctl("clean")
		return
	}

	var buf strings.Builder
	buf.WriteString("---\n")
	buf.WriteString(fmt.Sprintf("id: %s\n", bead.ID))
	buf.WriteString(fmt.Sprintf("title: %s\n", bead.Title))
	buf.WriteString(fmt.Sprintf("status: %s\n", bead.Status))
	if bead.Assignee != "" {
		buf.WriteString(fmt.Sprintf("assignee: %s\n", bead.Assignee))
	}
	buf.WriteString(fmt.Sprintf("blockers: %s\n", strings.Join(bead.Blockers, ", ")))
	buf.WriteString("---\n")
	if bead.Description != "" {
		desc, _ := strconv.Unquote(`"` + bead.Description + `"`)
		if desc == "" {
			desc = bead.Description
		}
		buf.WriteString(desc)
	}

	w.Addr(",")
	w.Write("data", []byte(buf.String()))
	w.Ctl("clean")
	w.Addr("0")
	w.Ctl("dot=addr")
	w.Ctl("show")
}

func openCommentsWindow(beadID string, mount string) error {
	w, err := acme.New()
	if err != nil {
		return err
	}
	w.Name(fmt.Sprintf("/Beads/Tasks/%s/comments", beadID))
	w.Write("tag", []byte("Get "))
	go handleCommentsWindow(w, beadID, mount)
	return nil
}

func openCommentWindow(beadID string, mount string) error {
	w, err := acme.New()
	if err != nil {
		return err
	}
	w.Name(fmt.Sprintf("/Beads/Tasks/%s/comment", beadID))
	w.Write("tag", []byte("Put "))
	go handleCommentWindow(w, beadID, mount)
	return nil
}

func handleCommentWindow(w *acme.Win, beadID string, mount string) {
	defer w.CloseFiles()
	for e := range w.EventChan() {
		if (e.C2 == 'x' || e.C2 == 'X') && string(e.Text) == "Put" {
			body, _ := w.ReadAll("body")
			text := strings.TrimSpace(string(body))
			if text != "" {
				cmd := fmt.Sprintf("comment %s '%s'", beadID, text)
				writeBeadsFile(filepath.Join(mount, "ctl"), []byte(cmd))
				w.Addr(",")
				w.Write("data", nil)
				w.Ctl("clean")
			}
		} else {
			w.WriteEvent(e)
		}
	}
}

func handleCommentsWindow(w *acme.Win, beadID string, mount string) {
	defer w.CloseFiles()
	refreshCommentsWindow(w, beadID, mount)
	for e := range w.EventChan() {
		if (e.C2 == 'x' || e.C2 == 'X') && string(e.Text) == "Get" {
			refreshCommentsWindow(w, beadID, mount)
		} else {
			w.WriteEvent(e)
		}
	}
}

func refreshCommentsWindow(w *acme.Win, beadID string, mount string) {
	data, _ := readBeadsFile(filepath.Join(mount, beadID, "comments"))
	var comments []struct {
		Author    string `json:"author"`
		Text      string `json:"text"`
		CreatedAt string `json:"created_at"`
	}
	json.Unmarshal(data, &comments)

	var buf strings.Builder
	if len(comments) == 0 {
		buf.WriteString("No comments.\n")
	} else {
		for _, c := range comments {
			buf.WriteString(fmt.Sprintf("--- %s (%s) ---\n%s\n\n", c.Author, c.CreatedAt, c.Text))
		}
	}
	w.Addr(",")
	w.Write("data", []byte(buf.String()))
	w.Ctl("clean")
}

func getBead(beadID string, mount string) (*Bead, error) {
	data, err := readBeadsFile(filepath.Join(mount, beadID, "json"))
	if err != nil {
		return nil, err
	}
	var bead Bead
	json.Unmarshal(data, &bead)
	return &bead, nil
}

func addBlocksDep(blockerID, blockedID string, mount string) error {
	cmd := fmt.Sprintf("dep %s %s", blockedID, blockerID)
	return writeBeadsFile(filepath.Join(mount, "ctl"), []byte(cmd))
}

func deleteBead(beadID string, mount string) error {
	return writeBeadsFile(filepath.Join(mount, "ctl"), []byte(fmt.Sprintf("delete %s", beadID)))
}

func claimBeadAsUser(beadID string, mount string) error {
	return writeBeadsFile(filepath.Join(mount, "ctl"), []byte(fmt.Sprintf("claim %s user", beadID)))
}

func unclaimBead(beadID string, mount string) error {
	return writeBeadsFile(filepath.Join(mount, "ctl"), []byte(fmt.Sprintf("unclaim %s", beadID)))
}

func completeBead(beadID string, mount string) error {
	return writeBeadsFile(filepath.Join(mount, "ctl"), []byte(fmt.Sprintf("complete %s", beadID)))
}

func failBead(beadID, reason string, mount string) error {
	return writeBeadsFile(filepath.Join(mount, "ctl"), []byte(fmt.Sprintf("fail %s %s", beadID, reason)))
}

func openBeadStatus(beadID string, mount string) error {
	return writeBeadsFile(filepath.Join(mount, "ctl"), []byte(fmt.Sprintf("open %s", beadID)))
}

func deferBead(beadID string, mount string) error {
	return writeBeadsFile(filepath.Join(mount, "ctl"), []byte(fmt.Sprintf("defer %s", beadID)))
}

func updateBead(beadID, content string, origBlockers []string, mount string) error {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return fmt.Errorf("missing frontmatter")
	}
	parts := strings.SplitN(content[3:], "---", 2)
	frontmatter := strings.TrimSpace(parts[0])
	description := ""
	if len(parts) > 1 {
		description = strings.TrimSpace(parts[1])
	}

	var title string
	var newBlockers []string
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "title:") {
			title = strings.TrimSpace(strings.TrimPrefix(line, "title:"))
		} else if strings.HasPrefix(line, "blockers:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "blockers:"))
			if val != "" {
				for _, b := range strings.Split(val, ",") {
					b = strings.TrimSpace(b)
					if b != "" {
						newBlockers = append(newBlockers, b)
					}
				}
			}
		}
	}

	if title != "" {
		writeBeadsFile(filepath.Join(mount, "ctl"), []byte(fmt.Sprintf("update %s title '%s'", beadID, shellEscape(title))))
	}
	if description != "" {
		writeBeadsFile(filepath.Join(mount, "ctl"), []byte(fmt.Sprintf("update %s description '%s'", beadID, shellEscape(description))))
	}

	origSet := make(map[string]bool)
	for _, b := range origBlockers {
		origSet[b] = true
	}
	newSet := make(map[string]bool)
	for _, b := range newBlockers {
		newSet[b] = true
	}
	for _, b := range newBlockers {
		if !origSet[b] {
			addBlocksDep(b, beadID, mount)
		}
	}
	for _, b := range origBlockers {
		if !newSet[b] {
			writeBeadsFile(filepath.Join(mount, "ctl"), []byte(fmt.Sprintf("undep %s %s", beadID, b)))
		}
	}
	return nil
}

func initBeads(prefix string, mount string) error {
	return writeBeadsFile(filepath.Join(mount, "ctl"), []byte(fmt.Sprintf("init %s", prefix)))
}

func listMounts() []string {
	data, err := readBeadsFile("mtab")
	if err != nil || len(data) == 0 {
		return nil
	}
	var mounts []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			mounts = append(mounts, fields[0])
		}
	}
	return mounts
}

func mountProject(cwd string) (string, error) {
	mount := fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	cmd := fmt.Sprintf("mount %s %s", cwd, mount)
	if err := writeBeadsFile("ctl", []byte(cmd)); err != nil {
		return "", err
	}
	return mount, nil
}

func umountProject(name string) error {
	return writeBeadsFile("ctl", []byte(fmt.Sprintf("umount %s", name)))
}

//go:build windows

package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var ErrSecureSessionsUnsupported = errors.New("secure filesystem sessions require NTFS and a verifiable Windows ACL")

const windowsFullFileAccess = 0x001F01FF

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create session directory parent: %w", err)
	}
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	sd, err := privateSecurityDescriptor(sid)
	if err != nil {
		return err
	}
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(pathPtr, &sa); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return fmt.Errorf("create protected session directory: %w", err)
	}
	h, err := openDirectoryNoFollow(path, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if err := requireNTFS(h); err != nil {
		return err
	}
	if err := rejectReparsePoint(h); err != nil {
		return err
	}
	info, err := fileIdentity(h)
	if err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return errors.New("session path is not a directory")
	}
	if err := validateWindowsACL(h, sid); err != nil {
		owner, ownerErr := getOwnerSID(h)
		if ownerErr != nil || !owner.Equals(sid) {
			return fmt.Errorf("session directory owner is not the application identity: %w", err)
		}
		if err := setPrivateACL(h, sid); err != nil {
			return fmt.Errorf("restrict existing session directory ACL: %w", err)
		}
		if err := validateWindowsACL(h, sid); err != nil {
			return fmt.Errorf("session directory ACL remains insecure: %w", err)
		}
	}
	pathHandle, err := openDirectoryNoFollow(path, windows.READ_CONTROL)
	if err != nil {
		return err
	}
	pathInfo, identityErr := fileIdentity(pathHandle)
	_ = windows.CloseHandle(pathHandle)
	if identityErr != nil || !sameWindowsFile(info, pathInfo) {
		return errors.New("session directory path changed during ACL validation")
	}
	return nil
}

func restrictSecretKeyFile(path string) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	h, err := openFileNoFollow(path, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if err := rejectReparsePoint(h); err != nil {
		return err
	}
	if err := validateWindowsACL(h, sid); err != nil {
		owner, ownerErr := getOwnerSID(h)
		if ownerErr != nil || !owner.Equals(sid) {
			return fmt.Errorf("secret-key file owner is not the application identity: %w", err)
		}
		if err := setPrivateACL(h, sid); err != nil {
			return err
		}
		if err := validateWindowsACL(h, sid); err != nil {
			return err
		}
	}
	return nil
}

func validateSecretKeyFilePermissions(path string, _ os.FileInfo) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	h, err := openFileNoFollow(path, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if err := rejectReparsePoint(h); err != nil {
		return err
	}
	if err := requireNTFS(h); err != nil {
		return err
	}
	return validateWindowsACL(h, sid)
}

func syncSecretKeyDirectory(string) error {
	// Windows does not expose a portable FlushFileBuffers operation for a
	// directory handle. The fully flushed NTFS file is atomically linked into
	// place; process restarts observe the completed directory operation.
	return nil
}

func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("get application identity SID: %w", err)
	}
	return user.User.Sid, nil
}

func privateSecurityDescriptor(appSID *windows.SID) (*windows.SECURITY_DESCRIPTOR, error) {
	systemSID, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		return nil, err
	}
	adminsSID, err := windows.StringToSid("S-1-5-32-544")
	if err != nil {
		return nil, err
	}
	principals := []*windows.SID{appSID, systemSID, adminsSID}
	seen := make(map[string]bool)
	var aces strings.Builder
	for _, sid := range principals {
		if seen[sid.String()] {
			continue
		}
		seen[sid.String()] = true
		aces.WriteString("(A;OICI;FA;;;")
		aces.WriteString(sid.String())
		aces.WriteByte(')')
	}
	sddl := "O:" + appSID.String() + "D:P" + aces.String()
	return windows.SecurityDescriptorFromString(sddl)
}

func openDirectoryNoFollow(path string, access uint32) (windows.Handle, error) {
	return openPathNoFollow(path, access, windows.FILE_FLAG_BACKUP_SEMANTICS)
}

func openFileNoFollow(path string, access uint32) (windows.Handle, error) {
	return openPathNoFollow(path, access, 0)
}

func openPathNoFollow(path string, access, flags uint32) (windows.Handle, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	h, err := windows.CreateFile(pathPtr, access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, flags|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("open protected filesystem path: %w", err)
	}
	return h, nil
}

func rejectReparsePoint(h windows.Handle) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("session path must not be a reparse point")
	}
	return nil
}

func fileIdentity(h windows.Handle) (windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return info, err
	}
	return info, nil
}

func sameWindowsFile(a, b windows.ByHandleFileInformation) bool {
	return a.VolumeSerialNumber == b.VolumeSerialNumber &&
		a.FileIndexHigh == b.FileIndexHigh && a.FileIndexLow == b.FileIndexLow
}

func requireNTFS(h windows.Handle) error {
	var fsName [32]uint16
	if err := windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, nil, &fsName[0], uint32(len(fsName))); err != nil {
		return fmt.Errorf("inspect session filesystem: %w", err)
	}
	name := windows.UTF16ToString(fsName[:])
	if !strings.EqualFold(name, "NTFS") {
		return fmt.Errorf("%w: found %s", ErrSecureSessionsUnsupported, name)
	}
	return nil
}

func getOwnerSID(h windows.Handle) (*windows.SID, error) {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	owner, _, err := sd.Owner()
	return owner, err
}

func validateWindowsACL(h windows.Handle, appSID *windows.SID) error {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(appSID) {
		return errors.New("filesystem object is not owned by the application identity")
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("filesystem object DACL is not protected from inheritance")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 3 {
		return errors.New("filesystem object must have exactly three explicit allow ACEs")
	}
	systemSID, _ := windows.StringToSid("S-1-5-18")
	adminsSID, _ := windows.StringToSid("S-1-5-32-544")
	expected := map[string]bool{appSID.String(): false, systemSID.String(): false, adminsSID.String(): false}
	if appSID.Equals(systemSID) {
		delete(expected, systemSID.String())
	}
	if appSID.Equals(adminsSID) {
		delete(expected, adminsSID.String())
	}
	if systemSID.Equals(adminsSID) {
		delete(expected, adminsSID.String())
	}
	if int(dacl.AceCount) != len(expected) {
		return errors.New("filesystem object has duplicate or missing identity ACEs")
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			ace.Header.AceFlags != windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE ||
			ace.Mask != windows.ACCESS_MASK(windowsFullFileAccess) {
			return errors.New("filesystem object has an unintended or inherited ACE")
		}
		aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		key := aceSID.String()
		if _, ok := expected[key]; !ok || expected[key] {
			return errors.New("filesystem object grants access to an unintended identity")
		}
		expected[key] = true
	}
	for _, present := range expected {
		if !present {
			return errors.New("filesystem object is missing a required identity ACE")
		}
	}
	return nil
}

func setPrivateACL(h windows.Handle, appSID *windows.SID) error {
	sd, err := privateSecurityDescriptor(appSID)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}

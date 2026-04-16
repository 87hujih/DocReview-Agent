package postgres

import "testing"

func TestUploadedFileRepoCreateAndGetByID(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	assistantRepo := NewAssistantRepo(pool)
	fileRepo := NewUploadedFileRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "上传原文件元数据测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	session, _, err := assistantRepo.CreateSessionWithMessages(ctx, "上传元数据", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	file, err := fileRepo.Create(ctx, UploadedFileCreateParams{
		ResourceID:       &resource.ID,
		SessionID:        &session.ID,
		OriginalFilename: "学生守则.pdf",
		ContentType:      "application/pdf",
		SizeBytes:        1234,
		SHA256:           "sha256-value",
		StorageKey:       "sh/sha256-value",
	})
	if err != nil {
		t.Fatalf("create uploaded file: %v", err)
	}

	found, err := fileRepo.GetByID(ctx, file.ID)
	if err != nil {
		t.Fatalf("get uploaded file: %v", err)
	}
	if found == nil {
		t.Fatal("expected uploaded file, got nil")
	}
	if found.OriginalFilename != "学生守则.pdf" {
		t.Fatalf("expected original filename %q, got %q", "学生守则.pdf", found.OriginalFilename)
	}
	if found.ResourceID == nil || *found.ResourceID != resource.ID {
		t.Fatalf("expected resource id %q, got %#v", resource.ID, found.ResourceID)
	}
	if found.SessionID == nil || *found.SessionID != session.ID {
		t.Fatalf("expected session id %q, got %#v", session.ID, found.SessionID)
	}
}

func TestUploadedFileRepoUpdateResourceID(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	fileRepo := NewUploadedFileRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "上传原文件绑定资源测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	file, err := fileRepo.Create(ctx, UploadedFileCreateParams{
		OriginalFilename: "学生守则.md",
		ContentType:      "text/markdown",
		SizeBytes:        12,
		SHA256:           "sha256-for-update",
		StorageKey:       "sh/sha256-for-update",
	})
	if err != nil {
		t.Fatalf("create uploaded file: %v", err)
	}

	if err := fileRepo.UpdateResourceID(ctx, file.ID, resource.ID); err != nil {
		t.Fatalf("update resource id: %v", err)
	}

	found, err := fileRepo.GetByID(ctx, file.ID)
	if err != nil {
		t.Fatalf("get uploaded file: %v", err)
	}
	if found.ResourceID == nil || *found.ResourceID != resource.ID {
		t.Fatalf("expected resource id %q, got %#v", resource.ID, found.ResourceID)
	}
}

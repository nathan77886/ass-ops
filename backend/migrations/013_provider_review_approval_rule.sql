INSERT INTO operation_approval_rules(
    resource_type,
    action,
    required_approver_roles,
    required_approval_count,
    expires_after_minutes,
    notification_channels,
    escalation_after_minutes,
    escalation_channels,
    priority,
    enabled,
    metadata
)
VALUES (
    'project_template_run',
    'project_template.provider_review.execute',
    ARRAY['admin','owner']::TEXT[],
    1,
    1440,
    ARRAY['ui']::TEXT[],
    120,
    ARRAY['ui']::TEXT[],
    10,
    true,
    '{"risk":"provider_review_execution","provider_api_mutation":"disabled"}'::jsonb
)
ON CONFLICT (resource_type, action) DO UPDATE
SET required_approver_roles=EXCLUDED.required_approver_roles,
    required_approval_count=EXCLUDED.required_approval_count,
    expires_after_minutes=EXCLUDED.expires_after_minutes,
    notification_channels=EXCLUDED.notification_channels,
    escalation_after_minutes=EXCLUDED.escalation_after_minutes,
    escalation_channels=EXCLUDED.escalation_channels,
    priority=EXCLUDED.priority,
    metadata=operation_approval_rules.metadata || EXCLUDED.metadata,
    enabled=true,
    updated_at=now();

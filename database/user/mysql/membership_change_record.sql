CREATE TABLE user_service.membership_change_record
(
    id                      BIGINT      NOT NULL COMMENT '会员权益变更记录标识，应用生成的正数雪花ID',
    user_id                 BIGINT      NOT NULL COMMENT '会员权益所属用户标识，引用user_service.user_profile.id',
    membership_id           BIGINT      NOT NULL COMMENT '当前会员权益记录标识，引用user_service.user_membership.id',
    order_id                BIGINT      NOT NULL COMMENT '本次权益变更对应订单标识，引用order_service.membership_order.id',
    change_type             INT         NOT NULL COMMENT '权益变更类型：10开通，20续费；由应用校验',
    before_membership_level INT         NULL DEFAULT NULL COMMENT '变更前会员等级；首次开通时为NULL',
    before_effective_at     DATETIME(3) NULL DEFAULT NULL COMMENT '变更前权益生效时间，UTC；首次开通时为NULL',
    before_expired_at       DATETIME(3) NULL DEFAULT NULL COMMENT '变更前权益到期时间，UTC；首次开通时为NULL',
    after_membership_level  INT         NOT NULL COMMENT '变更后会员等级，本例10表示标准会员',
    after_effective_at      DATETIME(3) NOT NULL COMMENT '变更后权益生效时间，UTC',
    after_expired_at        DATETIME(3) NOT NULL COMMENT '变更后权益到期时间，UTC',
    created_at              DATETIME(3) NOT NULL COMMENT '本次权益变更记录创建时间，UTC，由应用填写',
    PRIMARY KEY (id),
    UNIQUE KEY uk_membership_change_record_order_id (order_id),
    KEY idx_membership_change_record_user_id_created_at (user_id, created_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_0900_bin
    COMMENT ='会员权益变更历史，每行保存一笔会员订单引发的权益变更前后快照，只追加不修改';

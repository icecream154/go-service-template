CREATE TABLE user_service.user_membership
(
    id               BIGINT      NOT NULL COMMENT '当前会员权益记录标识，应用生成的正数雪花ID',
    user_id          BIGINT      NOT NULL COMMENT '所属用户标识，引用user_service.user_profile.id',
    membership_level INT         NOT NULL COMMENT '当前会员等级，本例10表示标准会员',
    effective_at     DATETIME(3) NOT NULL COMMENT '当前会员权益生效时间，UTC',
    expired_at       DATETIME(3) NOT NULL COMMENT '当前会员权益到期时间，UTC',
    created_at       DATETIME(3) NOT NULL COMMENT '会员权益记录创建时间，UTC，由应用填写',
    updated_at       DATETIME(3) NOT NULL COMMENT '会员权益最近修改时间，UTC，由应用填写',
    PRIMARY KEY (id),
    UNIQUE KEY uk_user_membership_user_id (user_id)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_0900_bin
    COMMENT ='用户当前会员权益，每个已开通过会员的用户最多一行，到期后保留记录';

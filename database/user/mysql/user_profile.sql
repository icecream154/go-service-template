CREATE TABLE user_service.user_profile
(
    id                  BIGINT       NOT NULL COMMENT '用户内部标识，应用生成的正数雪花ID',
    account_source      VARCHAR(32)  NOT NULL COMMENT '可信外部账户的完整身份命名空间代码，由服务端配置确定',
    external_account_id VARCHAR(128) NOT NULL COMMENT '来源内唯一的外部账户原始标识，按字符串保留，非本系统雪花ID',
    user_name           VARCHAR(64)  NOT NULL COMMENT '用户展示昵称，允许重名',
    avatar_url          VARCHAR(512) NULL     DEFAULT NULL COMMENT '用户头像URL；NULL表示尚未提供',
    is_enabled          TINYINT      NOT NULL DEFAULT 1 COMMENT '用户是否启用：0禁用，1启用；由应用校验',
    version             BIGINT       NOT NULL DEFAULT 0 COMMENT '资料修改乐观锁版本，每次受保护的资料修改加一',
    created_at          DATETIME(3)  NOT NULL COMMENT '用户资料创建时间，UTC，由应用填写',
    updated_at          DATETIME(3)  NOT NULL COMMENT '用户资料最近修改时间，UTC，由应用填写',
    PRIMARY KEY (id),
    UNIQUE KEY uk_user_profile_account_source_external_account_id
        (account_source, external_account_id)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_0900_bin
    COMMENT ='用户当前资料及固定外部账户映射，每个外部账户对应一个用户，昵称允许重名';

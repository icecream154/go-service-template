CREATE TABLE post_service.post
(
    id             BIGINT       NOT NULL COMMENT '帖子内部标识，应用生成的正数雪花ID',
    author_user_id BIGINT       NOT NULL COMMENT '帖子作者用户标识，引用user_service.user_profile.id',
    title          VARCHAR(200) NOT NULL COMMENT '帖子标题，由应用校验非空及长度',
    content        TEXT         NOT NULL COMMENT '帖子正文，由应用校验内容及大小',
    like_count     BIGINT       NOT NULL DEFAULT 0 COMMENT '用户尚未取消的点赞数量，来源于post_service.post_like；帖子软删除时保留',
    created_at     DATETIME(3)  NOT NULL COMMENT '帖子创建时间，UTC，由应用填写',
    updated_at     DATETIME(3)  NOT NULL COMMENT '帖子最近修改时间，UTC；正文、点赞计数或删除状态变更时由应用更新',
    deleted_at     DATETIME(3)  NULL     DEFAULT NULL COMMENT '帖子软删除时间，UTC；NULL表示未删除',
    PRIMARY KEY (id),
    KEY idx_post_author_user_id_deleted_at_created_at
        (author_user_id, deleted_at, created_at),
    KEY idx_post_deleted_at_created_at (deleted_at, created_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_0900_bin
    COMMENT ='帖子当前内容及状态，每行表示一篇帖子，采用软删除并保存尚未取消的点赞计数';

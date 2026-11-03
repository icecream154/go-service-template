CREATE TABLE post_service.post_like
(
    id         BIGINT      NOT NULL COMMENT '帖子点赞关系标识，应用生成的正数雪花ID',
    post_id    BIGINT      NOT NULL COMMENT '被点赞帖子标识，引用post_service.post.id',
    user_id    BIGINT      NOT NULL COMMENT '点赞用户标识，引用user_service.user_profile.id',
    created_at DATETIME(3) NOT NULL COMMENT '本次点赞建立时间，UTC，由应用填写',
    PRIMARY KEY (id),
    UNIQUE KEY uk_post_like_post_id_user_id (post_id, user_id),
    KEY idx_post_like_user_id_created_at_id_post_id
        (user_id, created_at, id, post_id)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_0900_bin
    COMMENT ='帖子当前尚未取消的点赞关系，每个帖子与用户组合最多一行，取消点赞时物理删除';

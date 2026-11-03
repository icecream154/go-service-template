CREATE TABLE user_service.user_follow
(
    id               BIGINT      NOT NULL COMMENT '用户关注关系标识，应用生成的正数雪花ID',
    follower_user_id BIGINT      NOT NULL COMMENT '关注方用户标识，引用user_service.user_profile.id',
    followed_user_id BIGINT      NOT NULL COMMENT '被关注方用户标识，引用user_service.user_profile.id',
    created_at       DATETIME(3) NOT NULL COMMENT '本次关注建立时间，UTC，由应用填写',
    PRIMARY KEY (id),
    UNIQUE KEY uk_user_follow_follower_user_id_followed_user_id
        (follower_user_id, followed_user_id),
    KEY idx_user_follow_follower_user_id_created_at
        (follower_user_id, created_at),
    KEY idx_user_follow_followed_user_id_created_at
        (followed_user_id, created_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_0900_bin
    COMMENT ='用户当前有向关注关系，每行表示关注方关注被关注方，取消关注时物理删除';
